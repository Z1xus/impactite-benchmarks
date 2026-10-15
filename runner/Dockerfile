FROM docker.io/library/golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /runner .

FROM scratch
COPY --from=build /runner /runner
ENTRYPOINT ["/runner"]
