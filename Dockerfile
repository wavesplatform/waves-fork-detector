FROM golang:alpine as builder

ARG APP=/forkdetector
ARG VER="v0.0.0-docker"

WORKDIR ${APP}

RUN apk add --no-cache make

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY *.go .
COPY api api
COPY chains chains
COPY loading loading
COPY peers peers
COPY version version

RUN go build -o build/bin/linux-amd64/forkdetector -ldflags="-X github.com/alexeykiselev/waves-fork-detector/version.version=${VER}" .

FROM alpine:latest
ARG APP=/forkdetector
ENV TZ=Etc/UTC
ENV APP_USER=forkdetector

ENV LOG_LEVEL=info
ENV CHAIN=mainnet
ENV DECLARED=0

STOPSIGNAL SIGINT

RUN addgroup -S $APP_USER && adduser -S $APP_USER -G $APP_USER

RUN apk add --no-cache bind-tools

USER $APP_USER
WORKDIR ${APP}

EXPOSE 8080
EXPOSE 6868

VOLUME /fd

COPY --from=builder ${APP}/build/bin/linux-amd64/forkdetector ${APP}/forkdetector

ENTRYPOINT ./forkdetector -db /fd -log-level $LOG_LEVEL -blockchain-type $CHAIN -declared-address $DECLARED -api 0.0.0.0:8080 -net 0.0.0.0:6868
