FROM golang:1.27-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy && go mod verify && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cinebase ./cmd/cinebase

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -S cinebase && adduser -S -G cinebase cinebase
WORKDIR /app
COPY --from=build /out/cinebase /usr/local/bin/cinebase
RUN mkdir -p /data && chown cinebase:cinebase /data
ENV CINEBASE_ADDR=:8097 CINEBASE_DATA_DIR=/data CINEBASE_MEDIA_ROOTS=/media
VOLUME ["/data", "/media"]
EXPOSE 8097
USER cinebase
ENTRYPOINT ["/usr/local/bin/cinebase"]
