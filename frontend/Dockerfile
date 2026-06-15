FROM nginx:1.27-alpine

# 단일 HTML 데모 파일을 nginx 기본 페이지로 배치
COPY ticketmong-front.html /usr/share/nginx/html/index.html

# nginx 기본 설정으로 80 포트 서빙
EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost/ || exit 1
