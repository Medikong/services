FROM nginx:1.27-alpine AS builder
COPY ticketmong-front.html /tmp/index.html

FROM nginx:1.27-alpine
RUN apk add --no-cache dumb-init && \
    addgroup -g 1001 -S appuser && \
    adduser -S appuser -G appuser -u 1001 && \
    chmod 777 /var/cache/nginx /var/run /var/log/nginx

COPY --from=builder /tmp/index.html /usr/share/nginx/html/index.html
COPY nginx.conf /etc/nginx/nginx.conf

USER appuser
EXPOSE 80
ENTRYPOINT ["/usr/sbin/dumb-init", "--"]
CMD ["nginx", "-g", "daemon off;"]
