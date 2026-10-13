ARG GO_IMAGE=golang:1.26-alpine
ARG RUNTIME_IMAGE=alpine:3.23
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/brainiac ./cmd/brainiac

FROM ${RUNTIME_IMAGE}
RUN addgroup -g 10001 brainiac && adduser -D -u 10001 -G brainiac brainiac && mkdir /data && chown brainiac:brainiac /data
COPY --from=build /out/brainiac /usr/local/bin/brainiac
USER 10001:10001
ENV LISTEN_IP=0.0.0.0 LISTEN_PORT=8765 DATA_PATH=/data/brainiac.db ACTIVITY_TIMEOUT=90m
EXPOSE 8765
ENTRYPOINT ["brainiac"]
