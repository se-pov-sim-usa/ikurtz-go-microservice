# syntax=docker/dockerfile:1.7

# ---- build stage ----
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/rockery .

# ---- runtime stage: no shell, no package manager, non-root ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rockery /rockery
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/rockery"]
