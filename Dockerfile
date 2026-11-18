# For later cloud use. Needs a host that allows outbound port 25.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /reachly ./cmd/reachly

FROM gcr.io/distroless/static
COPY --from=build /reachly /reachly
EXPOSE 8080
ENTRYPOINT ["/reachly"]
