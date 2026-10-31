// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func TestLogStoreTailBoundsAndRedaction(t *testing.T) {
	store, err := NewLogStore(512)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(store)
	logger.Info("CONNECTED", "peer_id", "peer-safe", "private_key", "must-not-appear")
	for _, message := range []string{
		"privateKey=must-not-appear", "ICE password=hunter2", "iceCredential=must-not-appear",
		"nonce=must-not-appear", "signature=must-not-appear", "rawPayload=must-not-appear",
	} {
		logger.Warn(message, "country", "NL")
	}
	logger.Error("CONNECT_FAILED", "reason", errors.New("ICE credential=must-not-appear"))
	for index := 0; index < 300; index++ {
		logger.Info(strings.Repeat("x", MaxLogMessageBytes*2), "state", index)
	}
	tail := store.Tail(MaxLogTailRecords)
	if len(tail.Records) > MaxLogTailRecords || proto.Size(tail) > MaxLogTailBytes {
		t.Fatalf("unbounded tail records=%d bytes=%d", len(tail.Records), proto.Size(tail))
	}
	for _, record := range tail.Records {
		if len(record.Message) > MaxLogMessageBytes {
			t.Fatalf("message length %d", len(record.Message))
		}
		encoded := strings.ToLower(record.String())
		if strings.Contains(encoded, "must-not-appear") || strings.Contains(encoded, "hunter2") {
			t.Fatalf("secret reached product log: %s", encoded)
		}
	}
	first := store.Tail(MaxLogTailRecords)
	if !first.Gap {
		t.Fatal("truncated ring tail did not report a gap")
	}
}

func TestLogStoreExportsSanitizedBoundedErrorText(t *testing.T) {
	store, err := NewLogStore(8)
	if err != nil {
		t.Fatal(err)
	}
	slog.New(store).Error("CONNECT_FAILED", "reason", errors.New("dial timeout"), "arbitrary", errors.New("not exported"))
	record := store.Tail(1).Records[0]
	if record.Fields["reason"] != "dial timeout" {
		t.Fatalf("sanitized reason=%q", record.Fields["reason"])
	}
	if _, exists := record.Fields["arbitrary"]; exists {
		t.Fatal("non-allowlisted error reached product logs")
	}
}

func TestLogStoreRedactsProductLinks(t *testing.T) {
	for _, prefix := range []string{"irvio-private:v1:", "IRVIO-PRIVATE:v1:", "irvio-bootstrap:v1:", "hydra-bootstrap:v1:"} {
		store, err := NewLogStore(8)
		if err != nil {
			t.Fatal(err)
		}
		slog.New(store).Warn(prefix+"must-not-appear", "reason", errors.New(prefix+"must-not-appear"))
		record := store.Tail(1).Records[0]
		if strings.Contains(record.String(), "must-not-appear") || record.Message != "[redacted sensitive log text]" || record.Fields["reason"] != "[redacted sensitive log text]" {
			t.Fatalf("product link escaped redaction: %v", record)
		}
	}
}

func TestLogStoreCursorContinuityGapAndIncarnation(t *testing.T) {
	store, err := NewLogStore(2)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(store)
	logger.Info("one")
	initial := store.Tail(10)
	tail, updates, cancel := store.Subscribe(initial.DaemonIncarnation, initial.LastSequence)
	defer cancel()
	if tail.Gap || len(tail.Records) != 0 {
		t.Fatalf("continuous cursor returned %+v", tail)
	}
	logger.Info("two")
	select {
	case record := <-updates:
		if record.Message != "two" || record.Sequence != initial.LastSequence+1 {
			t.Fatalf("live record %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("live log timeout")
	}
	logger.Info("three")
	logger.Info("four")
	gap, _, stop := store.Subscribe(initial.DaemonIncarnation, initial.LastSequence)
	stop()
	if !gap.Gap {
		t.Fatal("expired cursor did not report a gap")
	}
	restarted, err := NewLogStore(2)
	if err != nil {
		t.Fatal(err)
	}
	changed, _, stopChanged := restarted.Subscribe(initial.DaemonIncarnation, initial.LastSequence)
	stopChanged()
	if !changed.Gap || bytes.Equal(changed.DaemonIncarnation, initial.DaemonIncarnation) {
		t.Fatal("daemon incarnation change was not reported")
	}
}

func TestSlowLogSubscriberIsDroppedWithoutBlockingLogger(t *testing.T) {
	store, err := NewLogStore(512)
	if err != nil {
		t.Fatal(err)
	}
	_, updates, cancel := store.Subscribe(nil, 0)
	defer cancel()
	logger := slog.New(store)
	done := make(chan struct{})
	go func() {
		for index := 0; index < logSubscriberQueue+10; index++ {
			logger.Info("event", "state", index)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber blocked logging")
	}
	if got := store.SubscriberCount(); got != 0 {
		t.Fatalf("slow subscriber remains: %d", got)
	}
	for range updates {
	}
}

func TestMultipleLogSubscribersReceiveIndependently(t *testing.T) {
	store, err := NewLogStore(8)
	if err != nil {
		t.Fatal(err)
	}
	_, first, cancelFirst := store.Subscribe(nil, 0)
	defer cancelFirst()
	_, second, cancelSecond := store.Subscribe(nil, 0)
	defer cancelSecond()
	slog.New(store).Info("shared")
	for index, updates := range []<-chan *localv1.LogRecord{first, second} {
		select {
		case record := <-updates:
			if record.Message != "shared" {
				t.Fatalf("subscriber %d record=%v", index, record)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timed out", index)
		}
	}
}

func TestMultiHandlerPreservesPrimaryLogging(t *testing.T) {
	store, err := NewLogStore(10)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(NewMultiHandler(slog.NewTextHandler(&output, nil), store))
	logger.Log(context.Background(), slog.LevelInfo, "VISIBLE")
	if !strings.Contains(output.String(), "VISIBLE") || len(store.Tail(10).Records) != 1 {
		t.Fatalf("fanout output=%q tail=%v", output.String(), store.Tail(10))
	}
}

func TestLogAPIUsesSeparateBoundedSubscription(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	store, err := NewLogStore(512)
	if err != nil {
		t.Fatal(err)
	}
	_, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.Logs = store
	})
	client := dialTestClient(t, path)
	slog.New(store).Info("before")
	tail, err := client.GetRecentLogs(context.Background(), 20)
	if err != nil || len(tail.Records) != 1 {
		t.Fatalf("tail=%v err=%v", tail, err)
	}
	logClient := dialTestClient(t, path)
	missed, events, err := logClient.SubscribeLogs(context.Background(), tail.DaemonIncarnation, tail.LastSequence)
	if err != nil || missed.Gap || len(missed.Records) != 0 {
		t.Fatalf("subscribe tail=%v err=%v", missed, err)
	}
	slog.New(store).Warn("after")
	select {
	case event := <-events:
		if event.GetLogRecordAdded().GetRecord().GetMessage() != "after" {
			t.Fatalf("log event=%v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("log event timeout")
	}
}
