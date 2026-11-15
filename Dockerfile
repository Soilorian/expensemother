FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tesseract-ocr tesseract-ocr-fas tesseract-ocr-eng && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 10001 app && mkdir -p /data/receipts && chown -R app:app /data
COPY --from=build /server /usr/local/bin/server
USER app
ENV HTTP_ADDR=:7008 RECEIPT_DIR=/data/receipts
EXPOSE 7008
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=4 CMD curl --fail --silent --max-time 4 http://127.0.0.1:7008/readyz || exit 1
ENTRYPOINT ["server"]
