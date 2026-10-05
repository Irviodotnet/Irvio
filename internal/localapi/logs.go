// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package localapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

const (
	DefaultLogCapacity = 512
	MaxLogTailRecords  = 200
	MaxLogTailBytes    = 48 * 1024
	MaxLogMessageBytes = 512
	MaxLogFieldBytes   = 192
	MaxLogFields       = 12
	logSubscriberQueue = 64
)

type LogStore struct {
	mu          sync.Mutex
	incarnation []byte
	records     []*localv1.LogRecord
	capacity    int
	sequence    uint64
	nextID      uint64
	subscribers map[uint64]chan *localv1.LogRecord
}

func NewLogStore(capacity int) (*LogStore, error) {
	if capacity < 1 || capacity > 4096 {
		return nil, ErrInvalidConfig
	}
	incarnation := make([]byte, 16)
	if _, err := rand.Read(incarnation); err != nil {
		return nil, fmt.Errorf("create log daemon incarnation: %w", err)
	}
	return &LogStore{
		incarnation: incarnation,
		records:     make([]*localv1.LogRecord, 0, capacity),
		capacity:    capacity,
		subscribers: make(map[uint64]chan *localv1.LogRecord),
	}, nil
}

func (store *LogStore) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (store *LogStore) Handle(_ context.Context, record slog.Record) error {
	if record.Level < slog.LevelInfo {
		return nil
	}
	exported := &localv1.LogRecord{
		TimestampUnixMs: uint64(record.Time.UTC().UnixMilli()),
		Severity:        logSeverity(record.Level),
		Message:         sanitizeLogText(record.Message, MaxLogMessageBytes),
		Fields:          make(map[string]string),
	}
	fieldCount := 0
	record.Attrs(func(attribute slog.Attr) bool {
		if fieldCount == MaxLogFields {
			return false
		}
		key, value, ok := safeLogAttribute(attribute)
		if !ok {
			return true
		}
		exported.Fields[key] = value
		fieldCount++
		return true
	})
	if len(exported.Fields) == 0 {
		exported.Fields = nil
	}

	store.mu.Lock()
	store.sequence++
	exported.Sequence = store.sequence
	if len(store.records) == store.capacity {
		copy(store.records, store.records[1:])
		store.records[len(store.records)-1] = exported
	} else {
		store.records = append(store.records, exported)
	}
	for id, subscriber := range store.subscribers {
		select {
		case subscriber <- cloneLogRecord(exported):
		default:
			delete(store.subscribers, id)
			close(subscriber)
		}
	}
	store.mu.Unlock()
	return nil
}

func (store *LogStore) WithAttrs(attributes []slog.Attr) slog.Handler {
	return &productLogHandler{store: store, attributes: append([]slog.Attr(nil), attributes...)}
}

func (store *LogStore) WithGroup(group string) slog.Handler {
	return &productLogHandler{store: store, groups: []string{group}}
}

func (store *LogStore) Tail(limit int) *localv1.LogTail {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.tailLocked(limit, nil, 0)
}

func (store *LogStore) Subscribe(incarnation []byte, after uint64) (*localv1.LogTail, <-chan *localv1.LogRecord, func()) {
	store.mu.Lock()
	tail := store.tailLocked(MaxLogTailRecords, incarnation, after)
	store.nextID++
	id := store.nextID
	updates := make(chan *localv1.LogRecord, logSubscriberQueue)
	store.subscribers[id] = updates
	store.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			store.mu.Lock()
			if current, ok := store.subscribers[id]; ok {
				delete(store.subscribers, id)
				close(current)
			}
			store.mu.Unlock()
		})
	}
	return tail, updates, cancel
}

func (store *LogStore) tailLocked(limit int, incarnation []byte, after uint64) *localv1.LogTail {
	if limit < 1 || limit > MaxLogTailRecords {
		limit = MaxLogTailRecords
	}
	tail := &localv1.LogTail{
		LastSequence:      store.sequence,
		DaemonIncarnation: append([]byte(nil), store.incarnation...),
	}
	start := 0
	if len(incarnation) != 0 {
		if !bytes.Equal(incarnation, store.incarnation) {
			tail.Gap = true
		} else {
			if after > store.sequence {
				tail.Gap = true
			}
			for start < len(store.records) && store.records[start].Sequence <= after {
				start++
			}
			if len(store.records) != 0 && after+1 < store.records[0].Sequence {
				tail.Gap = true
				start = 0
			}
		}
	} else if after != 0 {
		tail.Gap = true
	}
	candidates := store.records[start:]
	if len(candidates) > limit {
		candidates = candidates[len(candidates)-limit:]
		tail.Gap = true
	}
	selected := make([]*localv1.LogRecord, 0, len(candidates))
	for index := len(candidates) - 1; index >= 0; index-- {
		candidate := cloneLogRecord(candidates[index])
		trial := append([]*localv1.LogRecord{candidate}, selected...)
		tail.Records = trial
		if proto.Size(tail) > MaxLogTailBytes {
			tail.Records = selected
			tail.Gap = true
			break
		}
		selected = trial
	}
	tail.Records = selected
	return tail
}

func (store *LogStore) Incarnation() []byte {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]byte(nil), store.incarnation...)
}

func (store *LogStore) SubscriberCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.subscribers)
}

type productLogHandler struct {
	store      *LogStore
	attributes []slog.Attr
	groups     []string
}

func (handler *productLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.store.Enabled(ctx, level)
}

func (handler *productLogHandler) Handle(ctx context.Context, record slog.Record) error {
	copyRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	copyRecord.AddAttrs(handler.attributes...)
	record.Attrs(func(attribute slog.Attr) bool {
		copyRecord.AddAttrs(attribute)
		return true
	})
	return handler.store.Handle(ctx, copyRecord)
}

func (handler *productLogHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	clone := *handler
	clone.attributes = append(append([]slog.Attr(nil), handler.attributes...), attributes...)
	clone.groups = append([]string(nil), handler.groups...)
	return &clone
}

func (handler *productLogHandler) WithGroup(group string) slog.Handler {
	clone := *handler
	clone.attributes = append([]slog.Attr(nil), handler.attributes...)
	clone.groups = append(append([]string(nil), handler.groups...), group)
	return &clone
}

type MultiHandler struct {
	handlers []slog.Handler
}

func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	filtered := make([]slog.Handler, 0, len(handlers))
	for _, handler := range handlers {
		if handler != nil {
			filtered = append(filtered, handler)
		}
	}
	return &MultiHandler{handlers: filtered}
}

func (handler *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, child := range handler.handlers {
		if child.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (handler *MultiHandler) Handle(ctx context.Context, record slog.Record) error {
	var result error
	for _, child := range handler.handlers {
		if child.Enabled(ctx, record.Level) {
			result = errors.Join(result, child.Handle(ctx, record.Clone()))
		}
	}
	return result
}

func (handler *MultiHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	children := make([]slog.Handler, len(handler.handlers))
	for index, child := range handler.handlers {
		children[index] = child.WithAttrs(attributes)
	}
	return NewMultiHandler(children...)
}

func (handler *MultiHandler) WithGroup(group string) slog.Handler {
	children := make([]slog.Handler, len(handler.handlers))
	for index, child := range handler.handlers {
		children[index] = child.WithGroup(group)
	}
	return NewMultiHandler(children...)
}

func logSeverity(level slog.Level) localv1.LogSeverity {
	switch {
	case level >= slog.LevelError:
		return localv1.LogSeverity_LOG_SEVERITY_ERROR
	case level >= slog.LevelWarn:
		return localv1.LogSeverity_LOG_SEVERITY_WARNING
	default:
		return localv1.LogSeverity_LOG_SEVERITY_INFO
	}
}

var safeLogKeys = map[string]struct{}{
	"active_peer": {}, "api_major": {}, "api_minor": {}, "awg_key_epoch": {},
	"country": {}, "endpoints": {}, "error": {}, "family": {}, "listen_addresses": {},
	"mux_port": {}, "network_generation": {}, "peer_id": {}, "port": {},
	"protected_mode": {}, "protection_adopted": {}, "remote_peer_id": {},
	"reason": {}, "runtime_generation": {}, "selected_endpoint": {}, "service": {},
	"session_generation": {}, "socket": {}, "state": {}, "transport_generation": {}, "underlay_ipv4": {},
	"underlay_ipv6": {},
}

var sensitiveLogFragments = []string{
	"private_key", "private key", "password", "credential", "ice_pwd", "ice-pwd",
	"ufrag", "nonce", "signature", "raw_payload", "raw payload", "signed_envelope",
	"signed envelope", "secret", "token",
}

func safeLogAttribute(attribute slog.Attr) (string, string, bool) {
	attribute.Value = attribute.Value.Resolve()
	key := strings.ToLower(attribute.Key)
	if _, ok := safeLogKeys[key]; !ok || containsSensitiveFragment(key) {
		return "", "", false
	}
	var value string
	switch attribute.Value.Kind() {
	case slog.KindString:
		value = attribute.Value.String()
	case slog.KindBool:
		value = fmt.Sprint(attribute.Value.Bool())
	case slog.KindInt64:
		value = fmt.Sprint(attribute.Value.Int64())
	case slog.KindUint64:
		value = fmt.Sprint(attribute.Value.Uint64())
	case slog.KindFloat64:
		value = fmt.Sprint(attribute.Value.Float64())
	case slog.KindDuration:
		value = attribute.Value.Duration().String()
	case slog.KindTime:
		value = attribute.Value.Time().UTC().Format(time.RFC3339)
	case slog.KindAny:
		reason, ok := attribute.Value.Any().(error)
		if !ok {
			return "", "", false
		}
		value = reason.Error()
	default:
		return "", "", false
	}
	value = sanitizeLogText(value, MaxLogFieldBytes)
	if value == "" {
		return "", "", false
	}
	return key, value, true
}

func sanitizeLogText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, "�"))
	if containsSensitiveFragment(strings.ToLower(value)) {
		return "[redacted sensitive log text]"
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func containsSensitiveFragment(value string) bool {
	for _, fragment := range sensitiveLogFragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	compact := strings.Map(func(value rune) rune {
		if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' {
			return value
		}
		return -1
	}, strings.ToLower(value))
	for _, fragment := range []string{
		"privatekey", "password", "credential", "icepwd", "icepassword",
		"ufrag", "nonce", "signature", "rawpayload", "signedenvelope",
		"secret", "token",
	} {
		if strings.Contains(compact, fragment) {
			return true
		}
	}
	return false
}

func cloneLogRecord(record *localv1.LogRecord) *localv1.LogRecord {
	if record == nil {
		return nil
	}
	return proto.Clone(record).(*localv1.LogRecord)
}
