IINA_PLUGIN := /Applications/IINA.app/Contents/MacOS/iina-plugin
DEVLINK := build/iina-airplay.iinaplugin-dev

.PHONY: helper test dev clean ffmpeg pack verify test-bundled

BUNDLED_FFMPEG := $(CURDIR)/build/ffmpeg/ffmpeg

helper:
	cd helper && go build -o ../plugin/bin/airplay-helper .

test:
	cd helper && go test ./...
	node --test 'plugin/tests/**/*.test.mjs'
	./packaging/tests/verify.test.sh

# Dev loop: helper from source, brew ffmpeg standing in for the bundled one,
# plugin symlinked into IINA. Restart IINA to pick up JS changes.
dev: helper
	ln -sf /opt/homebrew/bin/ffmpeg plugin/bin/ffmpeg
	mkdir -p build
	ln -sfn ../plugin $(DEVLINK)
	$(IINA_PLUGIN) link $(DEVLINK)

clean:
	rm -rf plugin/bin build

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
	rm -f build/iina-airplay.iinaplgz
	$(MAKE) ffmpeg
	./packaging/build-helper.sh
	./packaging/pack.sh
	./packaging/verify.sh build/iina-airplay.iinaplgz

verify:
	./packaging/verify.sh build/iina-airplay.iinaplgz
