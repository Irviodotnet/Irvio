// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package platformjournal

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/localapi"
)

func TestJournalRedactionDrainAndReopen(t *testing.T) {
	store, err := localapi.NewLogStore(512)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "logs")
	j, err := Start(store, path, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(store)
	logger.Info("SESSION_READY", "country", "NL", "private_key", "forbidden-attribute")
	logger.Warn("irvio-private:v1:forbidden-invitation")
	logger.Warn("FAILURE", "reason", "irvio-bootstrap:v1:forbidden-bundle")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if store.SubscriberCount() != 0 {
		t.Fatal("journal retained a log subscriber")
	}
	data := readSlots(t, path, "daemon", DefaultSlotBytes)
	if !bytes.Contains(data, []byte("SESSION_READY")) || bytes.Contains(data, []byte("forbidden")) || !bytes.Contains(data, []byte("redacted sensitive log text")) {
		t.Fatalf("missing or unsanitized journal: %s", data)
	}
	j, err = Start(store, path, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("AFTER_REOPEN")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(readSlots(t, path, "daemon", DefaultSlotBytes), []byte("AFTER_REOPEN")) {
		t.Fatal("reopened journal lost the final record")
	}
}

func TestJournalRotationBoundsAndPartialTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs")
	w, err := openWriter(path, "daemon", 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := w.write([]byte(fmt.Sprintf("%03d:%s\n", i, strings.Repeat("x", 48)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.write(bytes.Repeat([]byte("x"), 129)); err == nil {
		t.Fatal("oversize record accepted")
	}
	if err := w.write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	w, err = openWriter(path, "daemon", 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.write([]byte("restart\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	data := readSlots(t, path, "daemon", 128)
	if !bytes.Contains(data, []byte("099:")) || !bytes.Contains(data, []byte("partial\nrestart\n")) {
		t.Fatalf("rotation or partial-tail separation failed: %q", data)
	}
}

func TestJournalConcurrentProducersAndClose(t *testing.T) {
	store, err := localapi.NewLogStore(512)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "logs")
	j, err := Start(store, path, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				slog.New(store).Info("CONCURRENT_EVENT", "state", k)
			}
		}()
	}
	wg.Wait()
	slog.New(store).Info("FINAL_EVENT")
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := j.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if store.SubscriberCount() != 0 || !bytes.Contains(readSlots(t, path, "recovery", DefaultSlotBytes), []byte("FINAL_EVENT")) {
		t.Fatal("concurrent producer drain or subscription cleanup failed")
	}
}

func TestJournalRejectsUnsafeOrBusyFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs")
	w, err := openWriter(path, "daemon", 128)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.close() })
	if other, err := openWriter(path, "daemon", 128); err == nil {
		other.close()
		t.Fatal("concurrent writer accepted")
	}
	if err := w.files[0].Truncate(129); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	if other, err := openWriter(path, "daemon", 128); err == nil {
		other.close()
		t.Fatal("oversized existing slot accepted")
	}
	if info, err := os.Stat(filepath.Join(path, "daemon-0.jsonl")); err != nil || info.Size() != 129 {
		t.Fatal("rejected journal was silently truncated")
	}
	if _, err := Start(nil, path, "daemon"); err == nil {
		t.Fatal("nil store accepted")
	}
	store, _ := localapi.NewLogStore(4)
	if _, err := Start(store, path, "../escape"); err == nil {
		t.Fatal("unsafe filename prefix accepted")
	}
}

func readSlots(t *testing.T, path, prefix string, max int64) []byte {
	t.Helper()
	var result []byte
	for i := 0; i < slots; i++ {
		data, err := os.ReadFile(filepath.Join(path, fmt.Sprintf("%s-%d.jsonl", prefix, i)))
		if err != nil || int64(len(data)) > max {
			t.Fatalf("slot %d bytes=%d error=%v", i, len(data), err)
		}
		result = append(result, data...)
	}
	return result
}
