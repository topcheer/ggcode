FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /relay .

FROM alpine:3.19
COPY --from=builder /relay /relay
EXPOSE 8080
CMD ["/relay"]
