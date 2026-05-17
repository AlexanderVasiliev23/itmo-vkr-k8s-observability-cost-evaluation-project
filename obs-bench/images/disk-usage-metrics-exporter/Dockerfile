FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o disk_usage_metrics_exporter main.go

FROM alpine:3.19
COPY --from=build /app/disk_usage_metrics_exporter /disk_usage_metrics_exporter
EXPOSE 8080
ENTRYPOINT ["/disk_usage_metrics_exporter"]
