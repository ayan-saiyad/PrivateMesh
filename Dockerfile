# syntax=docker/dockerfile:1.7

FROM golang:1.26.5-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY gen ./gen
COPY internal ./internal

ARG SERVICE
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown

RUN test -n "${SERVICE}" && \
    CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X github.com/ayansaiyad/privatemesh/internal/version.Version=${VERSION} -X github.com/ayansaiyad/privatemesh/internal/version.Commit=${COMMIT} -X github.com/ayansaiyad/privatemesh/internal/version.BuildTime=${BUILD_TIME}" \
      -o /out/service \
      "./cmd/${SERVICE}"

RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/service /service
COPY --from=build --chown=nonroot:nonroot /out/data /var/lib/privatemesh

EXPOSE 8080 8081 8090 8091

USER nonroot:nonroot
ENTRYPOINT ["/service"]
