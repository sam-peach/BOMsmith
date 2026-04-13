# Target linux/amd64 explicitly — App Runner only runs x86_64 containers.
# FROM is resolved for the target platform automatically when building with
# `docker buildx build --platform linux/amd64`.

# Stage 1 — build the React frontend
FROM --platform=linux/amd64 node:20-alpine AS frontend
WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Stage 2 — build the Go binary
FROM --platform=linux/amd64 golang:1.24-alpine AS builder
WORKDIR /backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o bomsmith .

# Stage 3 — minimal runtime image
FROM --platform=linux/amd64 alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /backend/bomsmith .
COPY --from=frontend /frontend/dist ./static
RUN mkdir -p uploads
EXPOSE 8080
CMD ["./bomsmith"]
