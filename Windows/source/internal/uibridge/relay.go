// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package uibridge

import (
	"io"
	"net"
)

// Relay ends on either EOF; its caller must exit the helper process to release blocked stdio.
func Relay(conn net.Conn, input io.Reader, output io.Writer) error {
	defer conn.Close()
	done := make(chan error, 2)
	go func() {
		_, err := io.CopyBuffer(conn, input, make([]byte, 32*1024))
		done <- err
	}()
	go func() {
		_, err := io.CopyBuffer(output, conn, make([]byte, 32*1024))
		done <- err
	}()
	return <-done
}
