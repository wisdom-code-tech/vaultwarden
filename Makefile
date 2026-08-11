.PHONY: fetch validate build tools clean

fetch:
	./scripts/fetch-upstream.sh

validate:
	./scripts/validate.sh

tools:
	./scripts/bootstrap-fnpack.sh

build: tools fetch validate
	./scripts/build.sh

clean:
	rm -rf dist packaging/vaultwarden/app/server/runtime \
		packaging/vaultwarden/app/server/vaultwarden \
		packaging/vaultwarden/app/server/web-vault
