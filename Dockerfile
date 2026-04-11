FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY pkg/ pkg/
COPY core-service/ core-service/
COPY lms-service/ lms-service/
COPY marketing-service/ marketing-service/
COPY video-service/ video-service/
RUN cd lms-service && go build -o /app/lms-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/lms-service .
EXPOSE 8083
CMD ["./lms-service"]
