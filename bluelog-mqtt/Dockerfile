ARG BUILD_FROM

# The bridge is pure Go with cgo disabled, so the builder runs on the target
# architecture and the result drops straight into the Home Assistant base image.
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bluelog-mqtt .

FROM ${BUILD_FROM}
COPY --from=builder /out/bluelog-mqtt /usr/bin/bluelog-mqtt
COPY run.sh /run.sh
RUN chmod a+x /run.sh
CMD [ "/run.sh" ]
