FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o log-load-generator .

FROM alpine:3.19
COPY --from=build /app/log-load-generator /usr/local/bin/log-load-generator
ENTRYPOINT ["/usr/local/bin/log-load-generator"]
