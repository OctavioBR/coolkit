# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/coolkit .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/coolkit /coolkit
EXPOSE 8080 2345
USER nonroot:nonroot
ENTRYPOINT ["/coolkit"]
