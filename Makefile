IMG ?= quay.io/gordons/perilinkle:latest
CONTAINER_TOOL ?= podman

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	gofmt -w api cmd internal

.PHONY: build
build:
	go build ./cmd/perilinkle

.PHONY: image-build
image-build:
	$(CONTAINER_TOOL) build -t $(IMG) .

.PHONY: image-push
image-push:
	$(CONTAINER_TOOL) push $(IMG)

.PHONY: manifests
manifests:
	kubectl kustomize config

.PHONY: deploy
deploy:
	kubectl apply -k config/dev

.PHONY: undeploy
undeploy:
	kubectl delete -k config/dev

.PHONY: deploy-base
deploy-base:
	kubectl apply -k config
