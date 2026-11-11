IINA_PLUGIN := /Applications/IINA.app/Contents/MacOS/iina-plugin
DEVLINK := build/iina-airplay.iinaplugin-dev
# Where `iina-plugin link $(DEVLINK)` puts its symlink: IINA's plugins folder,
# named after the linked directory plus ".iinaplugin-dev" (iina-plugin's
# linkPlugin). Quote it in recipes, because "Application Support" has a space.
IINA_PLUGINS_DIR := $(HOME)/Library/Application Support/com.colliderli.iina/plugins
IINA_DEVLINK := $(IINA_PLUGINS_DIR)/$(notdir $(DEVLINK)).iinaplugin-dev

.PHONY: helper test test-package dev undev clean ffmpeg pack verify test-bundled

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
	./packaging/tests/check-published.test.sh
	./packaging/tests/release-notes.test.sh

# The gate on the configure line, run against the artifact that ships rather
# than against build/ffmpeg/ffmpeg. Both CI jobs run exactly this.
test-package:
	./packaging/test-package.sh $(PKG)

# Dev loop: helper from source, brew ffmpeg standing in for the bundled one,
# plugin linked into IINA. Restart IINA to pick up JS changes.
#
# A plugin DIRECTORY must carry its own manifest for IINA to load it, and the
# dev link points at plugin/. The real manifest lives at the repo root (IINA's
# update check reads it there), so plugin/Info.json is generated from it, with
# its own identifier and name and no ghRepo/ghVersion, so the dev build loads
# as "AirPlay (dev)" next to an installed copy instead of clashing with it.
# packaging/dev-manifest.mjs says why. It is gitignored and pack.sh never reads
# it. Regenerated on every run, so edits to the root Info.json carry over.
#
# Re-runnable: `iina-plugin link` refuses to replace an existing link (Cocoa
# error 516), and `iina-plugin unlink` only removes a link pointing at this
# exact path, so it misses one left by another checkout or worktree. Remove
# the link by name instead, and only if it is a symlink.
dev: helper
	ln -sf /opt/homebrew/bin/ffmpeg plugin/bin/ffmpeg
	node packaging/dev-manifest.mjs Info.json plugin/Info.json
	mkdir -p build
	ln -sfn ../plugin $(DEVLINK)
	@if [ -L "$(IINA_DEVLINK)" ]; then rm "$(IINA_DEVLINK)"; fi
	$(IINA_PLUGIN) link $(DEVLINK)
	@echo "dev: restart IINA. First time: enable \"AirPlay (dev)\" under Settings → Plugins."

# Takes the dev build out of IINA. Leaves its settings and enabled flag in
# place, so the next `make dev` comes back as it was.
undev:
	@if [ -L "$(IINA_DEVLINK)" ]; then rm "$(IINA_DEVLINK)" && echo "undev: removed $(IINA_DEVLINK); restart IINA"; \
	else echo "undev: no dev link in IINA's plugins folder"; fi

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
