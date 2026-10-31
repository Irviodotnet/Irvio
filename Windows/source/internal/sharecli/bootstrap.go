//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sharecli

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

const maxFileBytes = 8 << 20
const maxImagePixels = 12_000_000

func ReadBootstrap(input string) (string, error) {
	code := strings.TrimSpace(input)
	if !strings.HasPrefix(code, "irvio-bootstrap:") {
		file, err := os.OpenFile(input, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return "", fmt.Errorf("open bootstrap file: %w", err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
			return "", errors.New("bootstrap input must be a regular file no larger than 8 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
		if err != nil {
			return "", err
		}
		if len(data) > maxFileBytes {
			return "", errors.New("bootstrap file exceeds 8 MiB")
		}
		code = strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
		if !strings.HasPrefix(code, "irvio-bootstrap:") {
			config, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return "", errors.New("expected a network bootstrap text file or PNG/JPEG QR image")
			}
			if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
				return "", errors.New("QR image exceeds 12 megapixels")
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return "", errors.New("cannot decode QR image")
			}
			bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
			if err != nil {
				return "", errors.New("cannot read QR image")
			}
			result, err := qrcode.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true})
			if err != nil {
				return "", errors.New("no readable QR code in image")
			}
			code = strings.TrimSpace(result.GetText())
		}
	}
	if _, err := productbootstrap.Decode(code); err != nil {
		return "", fmt.Errorf("invalid network bootstrap (not a friend's invitation): %w", err)
	}
	return code, nil
}
