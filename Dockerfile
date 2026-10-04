FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gsm-server ./cmd/gsm-server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /gsm-server /gsm-server
COPY --from=build /src/internal/cassandra/migrations/ /migrations/
EXPOSE 8091
ENTRYPOINT ["/gsm-server"]