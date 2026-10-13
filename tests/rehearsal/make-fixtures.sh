#!/usr/bin/env bash
# make-fixtures.sh STORAGE_PATH — small, legally unencumbered media for a rehearsal node.
#
# REHEARSAL ONLY. Synthetic test patterns and tones generated with ffmpeg, plus a
# minimal valid EPUB, placed where Jellyfin (media) and Grimmory (books) expect them.
# Real candidate evidence uses the operator's legal fixtures, not these.
set -euo pipefail
storage="${1:?usage: make-fixtures.sh STORAGE_PATH}"
umask 002
movie="$storage/media/movies/Telos Test Film (2026)"
music="$storage/media/music/Telos Test Artist/Telos Test Album"
mkdir -p "$movie" "$music" "$storage/books"
[[ -f "$movie/Telos Test Film (2026).mp4" ]] || ffmpeg -loglevel error -y \
  -f lavfi -i "testsrc=duration=20:size=640x360:rate=24" -f lavfi -i "sine=frequency=440:duration=20" \
  -c:v libx264 -preset veryfast -pix_fmt yuv420p -profile:v baseline -g 48 -c:a aac -shortest -movflags +faststart \
  "$movie/Telos Test Film (2026).mp4"
[[ -f "$music/01 Test Tone.mp3" ]] || ffmpeg -loglevel error -y -f lavfi -i "sine=frequency=523:duration=30" \
  -metadata title="Test Tone" -metadata artist="Telos Test Artist" -metadata album="Telos Test Album" -c:a libmp3lame -b:a 96k "$music/01 Test Tone.mp3"
python3 - "$storage/books/Telos Test Book.epub" <<'PY'
import sys, zipfile
path = sys.argv[1]
container = '<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>'
opf = '''<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="id" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:7c9e6679-7425-40de-944b-e07fc1f90ae8</dc:identifier><dc:title>Telos Test Book</dc:title><dc:creator>Telos Test Author</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-10-13T00:00:00Z</meta></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="c1"/></spine></package>'''
nav = '<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter1.xhtml">Chapter One</a></li></ol></nav></body></html>'
chapter = '<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter One</title></head><body><h1>Chapter One</h1>' + "".join(f"<p>Rehearsal paragraph {i}: the quick brown fox jumps over the lazy dog.</p>" for i in range(40)) + "</body></html>"
with zipfile.ZipFile(path, "w") as z:
    z.writestr(zipfile.ZipInfo("mimetype"), "application/epub+zip", compress_type=zipfile.ZIP_STORED)
    z.writestr("META-INF/container.xml", container, compress_type=zipfile.ZIP_DEFLATED)
    z.writestr("OEBPS/content.opf", opf, compress_type=zipfile.ZIP_DEFLATED)
    z.writestr("OEBPS/nav.xhtml", nav, compress_type=zipfile.ZIP_DEFLATED)
    z.writestr("OEBPS/chapter1.xhtml", chapter, compress_type=zipfile.ZIP_DEFLATED)
PY
echo "make-fixtures: video, audio and epub under $storage"
