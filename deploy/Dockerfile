FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata sqlite && mkdir -p /app/vol
WORKDIR /app
COPY se8 migrate recompress /app/
RUN chmod +x /app/se8 /app/migrate /app/recompress
ENV SE8_VOL_DIR=/app/vol
ENV SE8_ADDR=0.0.0.0:8000
ENV TZ=Asia/Shanghai
VOLUME ["/app/vol"]
EXPOSE 8000
ENTRYPOINT ["/app/se8"]
