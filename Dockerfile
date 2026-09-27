# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/custodian ./cmd/custodian

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian"
LABEL org.opencontainers.image.description="Ben's on-demand Minecraft ECS Fargate custodian sidecar"
LABEL org.opencontainers.image.authors="https://github.com/bendoerr"
LABEL org.opencontainers.image.licenses=MIT

COPY --from=builder /out/custodian /custodian
USER nonroot:nonroot
ENTRYPOINT ["/custodian"]
