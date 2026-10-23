# Build statis, lalu jalankan di image distroless sebagai user nonroot.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/redis-cache-demo .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/redis-cache-demo /redis-cache-demo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/redis-cache-demo"]
