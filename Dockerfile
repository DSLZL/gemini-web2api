FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/gemini-web2api ./cmd/gemini-web2api

FROM alpine:3.20

RUN addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/gemini-web2api /usr/local/bin/gemini-web2api

ENV GEMINI_WEB2API_ADDR=:8081
EXPOSE 8081

USER app
ENTRYPOINT ["/usr/local/bin/gemini-web2api"]
