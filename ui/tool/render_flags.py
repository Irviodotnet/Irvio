#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

"""Render vendored flag-icons v7.3.2 SVGs to offline Flutter PNG assets."""
import pathlib
import sys
import cairo
import gi

gi.require_version("Rsvg", "2.0")
from gi.repository import Rsvg

source = pathlib.Path(sys.argv[1]) / "flags" / "4x3"
target = pathlib.Path(__file__).resolve().parents[1] / "assets" / "flags"
target.mkdir(parents=True, exist_ok=True)
for svg in sorted(source.glob("*.svg")):
    surface = cairo.ImageSurface(cairo.FORMAT_ARGB32, 96, 72)
    handle = Rsvg.Handle.new_from_file(str(svg))
    viewport = Rsvg.Rectangle()
    viewport.x, viewport.y, viewport.width, viewport.height = 0, 0, 96, 72
    handle.render_document(cairo.Context(surface), viewport)
    surface.write_to_png(str(target / (svg.stem + ".png")))
