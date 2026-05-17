FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o metrics_provider main.go

FROM alpine:3.19
COPY --from=build /app/metrics_provider /metrics_provider
EXPOSE 8080
ENTRYPOINT ["/metrics_provider"]
