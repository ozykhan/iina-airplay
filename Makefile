IINA_PLUGIN := /Applications/IINA.app/Contents/MacOS/iina-plugin
DEVLINK := build/iina-airplay.iinaplugin-dev

.PHONY: helper test test-package dev clean ffmpeg pack verify test-bundled

PKG := $(CURDIR)/build/iina-airplay.iinaplgz

BUNDLED_FFMPEG := $(CURDIR)/build/ffmpeg/ffmpeg

helper:
	cd helper && go build -o ../plugin/bin/airplay-helper .

test:
	cd helper && go test ./...
	node --test 'plugin/tests/**/*.test.mjs'
	./packaging/tests/verify.test.sh
	./packaging/tests/zip-plugin.test.sh
	./packaging/tests/check-release.test.sh
	./packaging/tests/pack-paths.test.sh
	./packaging/tests/release-notes.test.sh

# The gate on the configure line, run against the artifact that ships rather
# than against build/ffmpeg/ffmpeg. Both CI jobs run exactly this.
test-package:
	./packaging/test-package.sh $(PKG)

# Dev loop: helper from source, brew ffmpeg standing in for the bundled one,
# plugin symlinked into IINA. Restart IINA to pick up JS changes.
# Info.json lives at the repo root (IINA's update check reads it there), but a
# plugin DIRECTORY must carry its own manifest for IINA to load it at all — and
# the dev link points at plugin/. Symlink it in rather than keeping a second
# copy: one file stays authoritative, and plugin/Info.json is gitignored so the
# link never gets committed. A committed symlink would be worse than useless
# here — raw.githubusercontent.com serves a symlink's target path as text, so
# IINA's update check would parse "../Info.json" instead of JSON.
dev: helper
	ln -sf /opt/homebrew/bin/ffmpeg plugin/bin/ffmpeg
	ln -sf ../Info.json plugin/Info.json
	mkdir -p build
	ln -sfn ../plugin $(DEVLINK)
	$(IINA_PLUGIN) link $(DEVLINK)

clean:
	rm -rf plugin/bin build plugin/Info.json

# Builds the pinned LGPL ffmpeg. Slow the first time (tens of minutes) and a
# no-op afterwards until packaging/build-ffmpeg.sh changes.
ffmpeg:
	./packaging/build-ffmpeg.sh

# The gate on the ffmpeg recipe: runs the real-media e2e suite with the bundled
# binary in the pipeline. Fixtures still use Homebrew's ffmpeg, which has the
# encoders the bundled build deliberately excludes.
test-bundled:
	cd helper && IINA_AIRPLAY_FFMPEG=$(BUNDLED_FFMPEG) go test ./...

pack:
	rm -f $(PKG)
	$(MAKE) ffmpeg
	./packaging/build-helper.sh
	./packaging/pack.sh
	./packaging/verify.sh $(PKG)
	./packaging/test-package.sh $(PKG)

verify:
	./packaging/verify.sh $(PKG)
