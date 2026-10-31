// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package platformjournal

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

const DefaultSlotBytes int64 = 1 << 20
const slots = 4

type Journal struct {
	stop, done chan struct{}
	once       sync.Once
	err        error
}

// Start consumes only the existing redacted log stream; disk I/O never runs in a log producer.
func Start(store *localapi.LogStore, directory, prefix string) (*Journal, error) {
	if store == nil || (prefix != "daemon" && prefix != "recovery") {
		return nil, errors.New("invalid service journal configuration")
	}
	w, err := openWriter(directory, prefix, DefaultSlotBytes)
	if err != nil {
		return nil, err
	}
	tail, updates, cancel := store.Subscribe(nil, 0)
	j := &Journal{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(j.done)
		defer func() { j.err = errors.Join(j.err, w.close()) }()
		defer func() { cancel() }()
		incarnation := store.Incarnation()
		var after uint64
		writeTail := func(tail *localv1.LogTail) error {
			if tail.Gap {
				if err := w.record(incarnation, nil, true); err != nil {
					return err
				}
			}
			for _, record := range tail.Records {
				if record.Sequence <= after {
					continue
				}
				if err := w.record(incarnation, record, false); err != nil {
					return err
				}
				after = record.Sequence
			}
			return nil
		}
		for {
			if j.err = writeTail(tail); j.err != nil {
				return
			}
		stream:
			for {
				select {
				case <-j.stop:
					cancel()
					tail, _, cancel = store.Subscribe(incarnation, after)
					j.err = writeTail(tail)
					return
				case record, ok := <-updates:
					if !ok {
						break stream
					}
					if j.err = w.record(incarnation, record, false); j.err != nil {
						return
					}
					after = record.Sequence
				}
			}
			cancel()
			tail, updates, cancel = store.Subscribe(incarnation, after)
		}
	}()
	return j, nil
}

func (j *Journal) Close() error {
	j.once.Do(func() { close(j.stop) })
	<-j.done
	return j.err
}

type writer struct {
	directory *winfile.Directory
	files     []*os.File
	index     int
	size, max int64
}

func openWriter(path, prefix string, maximum int64) (*writer, error) {
	d, err := winfile.OpenDirectory(path, true)
	if err != nil {
		return nil, err
	}
	w := &writer{directory: d, max: maximum}
	fail := func(err error) (*writer, error) { return nil, errors.Join(err, w.close()) }
	var latest time.Time
	for i := 0; i < slots; i++ {
		f, err := d.OpenFile(fmt.Sprintf("%s-%d.jsonl", prefix, i), true, true)
		if err != nil {
			return fail(err)
		}
		w.files = append(w.files, f)
		if err := winfile.Lock(f); err != nil {
			return fail(err)
		}
		info, err := f.Stat()
		if err != nil {
			return fail(err)
		}
		if info.Size() > maximum {
			return fail(errors.New("existing journal slot exceeds size limit"))
		}
		if info.Size() > 0 && info.ModTime().After(latest) {
			latest, w.index, w.size = info.ModTime(), i, info.Size()
		}
	}
	f := w.files[w.index]
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fail(err)
	}
	if w.size > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], w.size-1); err != nil {
			return fail(err)
		}
		if last[0] != '\n' {
			if err := w.write([]byte{'\n'}); err != nil {
				return fail(err)
			}
		}
	}
	return w, nil
}

func (w *writer) record(incarnation []byte, record *localv1.LogRecord, gap bool) error {
	payload, err := json.Marshal(struct {
		Incarnation string             `json:"incarnation"`
		Gap         bool               `json:"gap,omitempty"`
		Record      *localv1.LogRecord `json:"record,omitempty"`
	}{hex.EncodeToString(incarnation), gap, record})
	if err != nil {
		return err
	}
	return w.write(append(payload, '\n'))
}

func (w *writer) write(payload []byte) error {
	if int64(len(payload)) > w.max {
		return errors.New("journal record exceeds slot size")
	}
	if w.size+int64(len(payload)) > w.max {
		w.index = (w.index + 1) % len(w.files)
		if err := w.files[w.index].Truncate(0); err != nil {
			return err
		}
		if _, err := w.files[w.index].Seek(0, io.SeekStart); err != nil {
			return err
		}
		w.size = 0
	}
	n, err := w.files[w.index].Write(payload)
	w.size += int64(n)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	return err
}

func (w *writer) close() error {
	var result error
	for _, f := range w.files {
		result = errors.Join(result, f.Close())
	}
	w.files = nil
	return errors.Join(result, w.directory.Close())
}
