FROM golang:1.16-alpine3.13 as builder
RUN mkdir /build
COPY . /build
WORKDIR /build
RUN go build .
FROM alpine:3.13
COPY --from=builder /build/support-gmail /usr/bin
ENTRYPOINT "support-gmail"
