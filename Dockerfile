FROM golang:1.26-alpine AS build
WORKDIR /src

# No BuildKit cache mounts here. Cloud Run source deploys build through Cloud
# Build's classic docker builder, which does not enable BuildKit, and a
# `RUN --mount=type=cache` fails there with an error that looks nothing like
# the cause. Layer caching on go.mod alone covers most of what those mounts
# bought us.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
ENV PORT=8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
