FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/se8 ./cmd/se8 && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=builder /out/se8 /app/se8
COPY --from=builder /out/migrate /app/migrate
ENV SE8_VOL_DIR=/app/vol
VOLUME ["/app/vol"]
EXPOSE 8000
ENTRYPOINT ["/app/se8"]
