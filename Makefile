IINA_PLUGIN := /Applications/IINA.app/Contents/MacOS/iina-plugin
DEVLINK := build/iina-airplay.iinaplugin-dev

.PHONY: helper test dev clean

helper:
	cd helper && go build -o ../plugin/bin/airplay-helper .

test:
	cd helper && go test ./...
	node --test 'plugin/tests/**/*.test.mjs'

# Dev loop: helper from source, brew ffmpeg standing in for the bundled one,
# plugin symlinked into IINA. Restart IINA to pick up JS changes.
dev: helper
	ln -sf /opt/homebrew/bin/ffmpeg plugin/bin/ffmpeg
	mkdir -p build
	ln -sfn ../plugin $(DEVLINK)
	$(IINA_PLUGIN) link $(DEVLINK)

clean:
	rm -rf plugin/bin build
