# Standalone build, context = this folder:
#   docker build --target server -t demo-host-server demo-host-server/
#   docker build --target seed   -t demo-host-seed   demo-host-server/
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed-walrus

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=build /out/server /server
EXPOSE 8081
ENTRYPOINT ["/server"]

# One-shot: pushes the schema and fills the engine with the demo library.
FROM gcr.io/distroless/static-debian12:nonroot AS seed
COPY --from=build /out/seed /seed
ENTRYPOINT ["/seed"]
