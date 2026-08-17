# --- 1) frontend -------------------------------------------------------------
FROM docker.io/library/node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- 2) backend (frontend embedded via go:embed) -----------------------------
FROM docker.io/library/golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /crewmate ./cmd/crewmate

# --- 3) runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /crewmate /crewmate
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/crewmate"]
