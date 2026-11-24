# helmstudio for Mac (docs/plan/31-mac-app.md): an Electron shell plus what it ships under Contents/Resources.
# Versions are owned by docs/plan/14-dependencies.md; the downloads are checked against their releases' sha256 files.
#   make test lint    the step runner's unit tests (commands faked) and the syntax checks
#   make app          dist.noindex/helmstudio.app (ad-hoc signed; limactl with the virtualization entitlement)
#   make dmg          dist.noindex/helmstudio.dmg
#   make run          the app from source on build/Resources (HELMSTUDIO_FAKE=1 HELMSTUDIO_HOME=/tmp/x to look around)

COLIMA_VERSION := v0.10.3
LIMA_VERSION   := 2.3.0
UV_VERSION     := 0.13.8

ROOT := $(abspath ..)
DL   := downloads
RES  := build/Resources
# dist.noindex: Spotlight skips *.noindex folders, so only the installed app shows in search
APP  := dist.noindex/helmstudio.app
GH   := https://github.com

.PHONY: test lint resources app dmg run clean

node_modules: package-lock.json
	npm ci --no-audit --no-fund
	node node_modules/electron/install.js   # the Electron binary (npm may skip install scripts)
	touch node_modules

test: node_modules
	node --test test/

lint:
	@for f in src/*.js src/ui/*.js test/*.js; do node --check $$f || exit 1; done
	@node -e 'JSON.parse(require("fs").readFileSync("stack.json"))'
	@plutil -lint -s limactl.entitlements
	@! grep -nE "shell: *true|execSync|exec\(\`|child_process'\)\.exec\b" src/*.js || { echo "commands run with an argument list, never a shell"; exit 1; }
	@echo lint ok

# ---------- downloads (each in its own folder; verified before use)

$(DL)/colima/colima-Darwin-arm64:
	mkdir -p $(@D)
	cd $(@D) && curl -fsSLO $(GH)/abiosoft/colima/releases/download/$(COLIMA_VERSION)/colima-Darwin-arm64 \
	  && curl -fsSLO $(GH)/abiosoft/colima/releases/download/$(COLIMA_VERSION)/colima-Darwin-arm64.sha256sum \
	  && shasum -a 256 -c colima-Darwin-arm64.sha256sum || { rm -f colima-Darwin-arm64; exit 1; }

$(DL)/lima/lima-$(LIMA_VERSION)-Darwin-arm64.tar.gz:
	mkdir -p $(@D)
	cd $(@D) && curl -fsSLO $(GH)/lima-vm/lima/releases/download/v$(LIMA_VERSION)/lima-$(LIMA_VERSION)-Darwin-arm64.tar.gz \
	  && curl -fsSLO $(GH)/lima-vm/lima/releases/download/v$(LIMA_VERSION)/SHA256SUMS \
	  && grep ' lima-$(LIMA_VERSION)-Darwin-arm64.tar.gz$$' SHA256SUMS | shasum -a 256 -c - || { rm -f lima-*.tar.gz; exit 1; }

$(DL)/uv/uv-aarch64-apple-darwin.tar.gz:
	mkdir -p $(@D)
	cd $(@D) && curl -fsSLO $(GH)/astral-sh/uv/releases/download/$(UV_VERSION)/uv-aarch64-apple-darwin.tar.gz \
	  && curl -fsSLO $(GH)/astral-sh/uv/releases/download/$(UV_VERSION)/uv-aarch64-apple-darwin.tar.gz.sha256 \
	  && shasum -a 256 -c uv-aarch64-apple-darwin.tar.gz.sha256 || { rm -f uv-*.tar.gz; exit 1; }

# ---------- what the app ships (build/Resources): the VM tools, uv, helm, the runner code, the stack files

resources: $(DL)/colima/colima-Darwin-arm64 $(DL)/lima/lima-$(LIMA_VERSION)-Darwin-arm64.tar.gz $(DL)/uv/uv-aarch64-apple-darwin.tar.gz
	rm -rf $(RES) && mkdir -p $(RES)/vm $(RES)/bin $(RES)/runner $(RES)/stack
	tar -xzf $(DL)/lima/lima-$(LIMA_VERSION)-Darwin-arm64.tar.gz -C $(RES)/vm
	install -m 755 $(DL)/colima/colima-Darwin-arm64 $(RES)/vm/bin/colima
	tar -xzf $(DL)/uv/uv-aarch64-apple-darwin.tar.gz -C $(DL)/uv && install -m 755 $(DL)/uv/uv-aarch64-apple-darwin/uv $(RES)/bin/uv
	cd $(ROOT)/cli && CGO_ENABLED=0 go build -o $(abspath $(RES))/bin/helm .   # decision 10: helm for the MCP
	# the runner code and the contracts it reads (tracked files only: no venvs, .env, tests), plus the built binaries
	cd $(ROOT) && git ls-files -z runner_service contracts | grep -zv '^runner_service/tests/' \
	  | tar --null -T - -cf - | tar -xf - -C $(abspath $(RES))/runner
	@for b in vendor/h3c/h3 vendor/zonos3/build/zonos3-server; do \
	  if [ -x $(ROOT)/runner_service/$$b ]; then mkdir -p $(RES)/runner/runner_service/$$(dirname $$b) && \
	    cp $(ROOT)/runner_service/$$b $(RES)/runner/runner_service/$$b; \
	  else echo "note: runner_service/$$b is not built (make h3 / make zonos3 at the root): the native packs will not run"; fi; done
	# make-secrets, the one-shots' scripts (nats-setup, storage-setup) and the deploy files (compose, nats.conf it rewrites); never deploy/secrets*
	cd $(ROOT) && git ls-files -z deploy .tools/make-secrets .tools/nats-setup .tools/storage-setup | tar --null -T - -cf - | tar -xf - -C $(abspath $(RES))/stack

# ---------- the app: Electron's own bundle, renamed, our code in Resources/app

app: node_modules resources
	rm -rf $(APP) && mkdir -p dist.noindex
	ditto node_modules/electron/dist/Electron.app $(APP)
	rm -f $(APP)/Contents/Resources/default_app.asar
	mkdir -p $(APP)/Contents/Resources/app
	cp -R package.json stack.json src assets $(APP)/Contents/Resources/app/
	ditto $(RES) $(APP)/Contents/Resources
	cp assets/icon.icns $(APP)/Contents/Resources/electron.icns
	plutil -replace CFBundleName -string helmstudio $(APP)/Contents/Info.plist
	plutil -replace CFBundleDisplayName -string helmstudio $(APP)/Contents/Info.plist
	plutil -replace CFBundleIdentifier -string com.helmstudio.app $(APP)/Contents/Info.plist
	plutil -replace CFBundleShortVersionString -string $$(node -p 'require("./package.json").version') $(APP)/Contents/Info.plist
	# ad-hoc signatures: every Mach-O we added, limactl with the virtualization entitlement (vz), then the app
	find $(APP)/Contents/Resources -type f -perm -u+x -not -name '*.sh' | while read -r f; do \
	  file -b "$$f" | grep -q Mach-O && codesign --force --sign - "$$f" >/dev/null; done; :
	codesign --force --sign - --entitlements limactl.entitlements $(APP)/Contents/Resources/vm/bin/limactl
	codesign --force --deep --sign - $(APP)
	codesign --verify --deep --strict $(APP)
	codesign -d --entitlements - $(APP)/Contents/Resources/vm/bin/limactl 2>&1 | grep -q com.apple.security.virtualization
	@echo "built $(APP)"

dmg: app
	rm -rf build/dmg dist.noindex/helmstudio.dmg && mkdir -p build/dmg
	ditto $(APP) build/dmg/helmstudio.app && ln -s /Applications build/dmg/Applications
	hdiutil create -volname helmstudio -srcfolder build/dmg -ov -format UDZO dist.noindex/helmstudio.dmg
	rm -rf build/dmg

run: node_modules
	node_modules/.bin/electron .

clean:
	rm -rf build dist.noindex
