// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package peerstore provides the SQLite-backed IRVIO peer directory.
package peerstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 3

// ErrStaleAnnouncement indicates that an announcement does not supersede the stored value.
var (
	ErrStaleAnnouncement    = errors.New("stale announcement")
	ErrAnnouncementConflict = errors.New("conflicting announcement version")
	ErrStaleAWGBinding      = errors.New("stale AWG key binding")
)

// Directory owns a node's SQLite peer database.
type Directory struct {
	db *sql.DB
}

// Peer contains the queryable fields of a verified peer announcement.
type Peer struct {
	NodeID          string
	ProtocolVersion uint32
	Capabilities    uint64
	Country         string
	AWGPublicKey    []byte
	AWGKeyEpoch     uint64
	FirstSeen       time.Time
	LastSeen        time.Time
	LastConnected   *time.Time
	ExpiresAt       time.Time
}

// Announcement contains a verified signed announcement stored for a peer.
type Announcement struct {
	NodeID         string
	Incarnation    uint64
	DescriptorID   []byte
	Epoch          []byte
	Sequence       uint64
	SignedEnvelope []byte
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Open opens a directory, configures SQLite, and applies schema migrations.
func Open(ctx context.Context, path string) (*Directory, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open peer directory: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure peer directory (%s): %w", pragma, err)
		}
	}
	d := &Directory{db: db}
	if err := d.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *Directory) migrate(ctx context.Context) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin peer directory migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema version table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version)
		SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_version)`); err != nil {
		return fmt.Errorf("initialize schema version: %w", err)
	}
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported %d", version, schemaVersion)
	}
	if version < 1 {
		statements := []string{
			`CREATE TABLE peers (
				node_id TEXT PRIMARY KEY,
				protocol_version INTEGER NOT NULL,
				capabilities INTEGER NOT NULL,
				country TEXT,
				first_seen INTEGER NOT NULL,
				last_seen INTEGER NOT NULL,
				last_connected INTEGER,
				expires_at INTEGER
			)`,
			`CREATE TABLE announcements (
				node_id TEXT PRIMARY KEY,
				epoch BLOB NOT NULL,
				sequence INTEGER NOT NULL,
				signed_envelope BLOB NOT NULL,
				created_at INTEGER NOT NULL,
				expires_at INTEGER NOT NULL,
				FOREIGN KEY(node_id) REFERENCES peers(node_id)
			)`,
			`CREATE TABLE service_cache (
				service_key TEXT NOT NULL,
				node_id TEXT NOT NULL,
				discovered_at INTEGER NOT NULL,
				PRIMARY KEY(service_key, node_id)
			)`,
			"UPDATE schema_version SET version = 1",
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema migration 1: %w", err)
			}
		}
	}
	if version < 2 {
		for _, statement := range []string{
			"ALTER TABLE peers ADD COLUMN awg_public_key BLOB",
			"ALTER TABLE peers ADD COLUMN awg_key_epoch INTEGER NOT NULL DEFAULT 0",
			"UPDATE schema_version SET version = 2",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema migration 2: %w", err)
			}
		}
	}
	if version < 3 {
		for _, statement := range []string{
			"ALTER TABLE announcements ADD COLUMN incarnation INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE announcements ADD COLUMN descriptor_id BLOB",
			"UPDATE schema_version SET version = 3",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema migration 3: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit peer directory migration: %w", err)
	}
	return nil
}

// StoreVerified atomically stores a peer and its verified signed announcement.
func (d *Directory) StoreVerified(ctx context.Context, peer Peer, announcement Announcement) error {
	if peer.NodeID == "" || announcement.NodeID != peer.NodeID {
		return fmt.Errorf("peer and announcement NodeID mismatch")
	}
	if len(peer.AWGPublicKey) != 0 && len(peer.AWGPublicKey) != 32 || len(peer.AWGPublicKey) == 0 && peer.AWGKeyEpoch != 0 || len(peer.AWGPublicKey) == 32 && peer.AWGKeyEpoch == 0 {
		return fmt.Errorf("invalid AWG key binding")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin store verified announcement: %w", err)
	}
	defer tx.Rollback()

	var storedEpoch, storedDescriptorID []byte
	var storedIncarnation, storedSequence uint64
	var storedCreatedAt int64
	var storedAWGKey []byte
	var storedAWGEpoch uint64
	err = tx.QueryRowContext(ctx,
		"SELECT a.epoch, a.incarnation, a.sequence, a.descriptor_id, a.created_at, p.awg_public_key, p.awg_key_epoch FROM announcements a JOIN peers p ON p.node_id = a.node_id WHERE a.node_id = ?",
		peer.NodeID,
	).Scan(&storedEpoch, &storedIncarnation, &storedSequence, &storedDescriptorID, &storedCreatedAt, &storedAWGKey, &storedAWGEpoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read current announcement: %w", err)
	}
	if err == nil {
		if announcement.Incarnation != 0 || storedIncarnation != 0 {
			if announcement.Incarnation < storedIncarnation ||
				announcement.Incarnation == storedIncarnation && announcement.Sequence < storedSequence {
				return ErrStaleAnnouncement
			}
			if announcement.Incarnation == storedIncarnation && announcement.Sequence == storedSequence {
				if len(announcement.DescriptorID) != 0 && bytes.Equal(announcement.DescriptorID, storedDescriptorID) {
					return nil
				}
				return ErrAnnouncementConflict
			}
		} else {
			if bytes.Equal(storedEpoch, announcement.Epoch) && announcement.Sequence <= storedSequence {
				return ErrStaleAnnouncement
			}
			if !bytes.Equal(storedEpoch, announcement.Epoch) && announcement.CreatedAt.Unix() <= storedCreatedAt {
				return ErrStaleAnnouncement
			}
		}
		if peer.AWGKeyEpoch < storedAWGEpoch || peer.AWGKeyEpoch == storedAWGEpoch && !bytes.Equal(peer.AWGPublicKey, storedAWGKey) {
			return ErrStaleAWGBinding
		}
	}

	lastSeenTime := peer.LastSeen
	if lastSeenTime.IsZero() {
		lastSeenTime = time.Now()
	}
	now := lastSeenTime.Unix()
	firstSeenTime := peer.FirstSeen
	if firstSeenTime.IsZero() {
		firstSeenTime = lastSeenTime
	}
	firstSeen := firstSeenTime.Unix()
	var lastConnected any
	if peer.LastConnected != nil {
		lastConnected = peer.LastConnected.Unix()
	}
	var expiresAt any
	if !peer.ExpiresAt.IsZero() {
		expiresAt = peer.ExpiresAt.Unix()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO peers(
		node_id, protocol_version, capabilities, country, awg_public_key, awg_key_epoch, first_seen, last_seen, last_connected, expires_at
	) VALUES(?, ?, ?, NULLIF(?, ''), NULLIF(?, X''), ?, ?, ?, ?, ?)
	ON CONFLICT(node_id) DO UPDATE SET
		protocol_version=excluded.protocol_version,
		capabilities=excluded.capabilities,
		country=excluded.country,
		awg_public_key=excluded.awg_public_key,
		awg_key_epoch=excluded.awg_key_epoch,
		last_seen=excluded.last_seen,
		last_connected=COALESCE(excluded.last_connected, peers.last_connected),
		expires_at=excluded.expires_at`,
		peer.NodeID, peer.ProtocolVersion, int64(peer.Capabilities), peer.Country, peer.AWGPublicKey, peer.AWGKeyEpoch,
		firstSeen, now, lastConnected, expiresAt,
	); err != nil {
		return fmt.Errorf("upsert peer: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO announcements(
		node_id, epoch, incarnation, sequence, descriptor_id, signed_envelope, created_at, expires_at
	) VALUES(?, ?, ?, ?, NULLIF(?, X''), ?, ?, ?)
	ON CONFLICT(node_id) DO UPDATE SET
		epoch=excluded.epoch,
		incarnation=excluded.incarnation,
		sequence=excluded.sequence,
		descriptor_id=excluded.descriptor_id,
		signed_envelope=excluded.signed_envelope,
		created_at=excluded.created_at,
		expires_at=excluded.expires_at`,
		announcement.NodeID, announcement.Epoch, announcement.Incarnation, announcement.Sequence, announcement.DescriptorID,
		announcement.SignedEnvelope, announcement.CreatedAt.Unix(), announcement.ExpiresAt.Unix(),
	); err != nil {
		return fmt.Errorf("upsert announcement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit verified announcement: %w", err)
	}
	return nil
}

// RecordService records a DHT service discovery result.
func (d *Directory) RecordService(ctx context.Context, serviceKey, nodeID string, discoveredAt time.Time) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO service_cache(service_key, node_id, discovered_at)
		VALUES(?, ?, ?)
		ON CONFLICT(service_key, node_id) DO UPDATE SET discovered_at=excluded.discovered_at`,
		serviceKey, nodeID, discoveredAt.Unix())
	if err != nil {
		return fmt.Errorf("record discovered service: %w", err)
	}
	return nil
}

// Peer returns one peer by NodeID.
func (d *Directory) Peer(ctx context.Context, nodeID string) (Peer, error) {
	var result Peer
	var capabilities int64
	var country sql.NullString
	var awgKey []byte
	var awgEpoch uint64
	var firstSeen, lastSeen int64
	var lastConnected, expiresAt sql.NullInt64
	err := d.db.QueryRowContext(ctx, `SELECT node_id, protocol_version, capabilities, country,
		awg_public_key, awg_key_epoch, first_seen, last_seen, last_connected, expires_at FROM peers WHERE node_id = ?`, nodeID).Scan(
		&result.NodeID, &result.ProtocolVersion, &capabilities, &country,
		&awgKey, &awgEpoch, &firstSeen, &lastSeen, &lastConnected, &expiresAt,
	)
	if err != nil {
		return Peer{}, fmt.Errorf("get peer %s: %w", nodeID, err)
	}
	result.Capabilities = uint64(capabilities)
	result.Country = country.String
	result.AWGPublicKey = append([]byte(nil), awgKey...)
	result.AWGKeyEpoch = awgEpoch
	result.FirstSeen = time.Unix(firstSeen, 0)
	result.LastSeen = time.Unix(lastSeen, 0)
	if lastConnected.Valid {
		value := time.Unix(lastConnected.Int64, 0)
		result.LastConnected = &value
	}
	if expiresAt.Valid {
		result.ExpiresAt = time.Unix(expiresAt.Int64, 0)
	}
	return result, nil
}

// SignedAnnouncement returns the signed envelope stored for a peer.
func (d *Directory) SignedAnnouncement(ctx context.Context, nodeID string) ([]byte, error) {
	var envelope []byte
	if err := d.db.QueryRowContext(ctx,
		"SELECT signed_envelope FROM announcements WHERE node_id = ?", nodeID,
	).Scan(&envelope); err != nil {
		return nil, fmt.Errorf("get signed announcement for %s: %w", nodeID, err)
	}
	return envelope, nil
}

// SchemaVersion returns the active database schema version.
func (d *Directory) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := d.db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// Close closes the peer directory.
func (d *Directory) Close() error {
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("close peer directory: %w", err)
	}
	return nil
}
