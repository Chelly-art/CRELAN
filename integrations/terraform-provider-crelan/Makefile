build:
	go build -o terraform-provider-crelan .

install-local: build
	mkdir -p "$(HOME)/.terraform.d/plugins/registry.terraform.io/crelan/crelan/0.1.0/$$(go env GOOS)_$$(go env GOARCH)"
	cp terraform-provider-crelan "$(HOME)/.terraform.d/plugins/registry.terraform.io/crelan/crelan/0.1.0/$$(go env GOOS)_$$(go env GOARCH)/terraform-provider-crelan_v0.1.0"
