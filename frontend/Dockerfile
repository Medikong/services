FROM nginx:1.27-alpine AS builder
COPY ticketmong-front.html /tmp/index.html

FROM nginx:1.27-alpine
RUN chmod 777 /var/cache/nginx /var/run /var/log/nginx

COPY --from=builder /tmp/index.html /usr/share/nginx/html/index.html
COPY nginx.conf /etc/nginx/nginx.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
