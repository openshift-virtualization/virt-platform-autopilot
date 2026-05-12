# Build stage
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

ARG TARGETARCH

WORKDIR /workspace

# Copy go mod files
COPY go.mod go.mod
COPY go.sum go.sum

# Cache dependencies
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY assets/ assets/

# Build the operator manager
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# Build the CSV generator (invoked by HCO's build-manifests.sh to produce the
# OLM ClusterServiceVersion contributed by this operator to the unified HCO bundle)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -o csv-generator cmd/csv-generator/main.go

# Runtime stage
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/csv-generator /usr/bin/csv-generator

USER 65532:65532

ENTRYPOINT ["/manager"]
