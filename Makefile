.PHONY: fetch gateway validate build tools clean

fetch:
	./scripts/fetch-upstream.sh

gateway:
	./scripts/build-gateway.sh

validate:
	./scripts/validate.sh

tools:
	./scripts/bootstrap-fnpack.sh

build: tools gateway fetch validate
	./scripts/build.sh

clean:
	rm -rf dist packaging/vaultwarden/app/server/runtime \
		packaging/vaultwarden/app/server/vaultwarden \
		packaging/vaultwarden/app/server/gateway-proxy \
		packaging/vaultwarden/app/server/web-vault
