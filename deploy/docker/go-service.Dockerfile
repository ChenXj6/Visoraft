FROM golang:1.25-alpine AS build

ARG SERVICE
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

RUN test -n "${SERVICE}" \
    && CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X github.com/visoraft/visoraft/internal/buildinfo.Version=${VERSION} -X github.com/visoraft/visoraft/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/visoraft \
      "./cmd/${SERVICE}"

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 visoraft \
    && adduser -S -D -H -u 10001 -G visoraft visoraft

COPY --from=build /out/visoraft /usr/local/bin/visoraft

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/visoraft"]
