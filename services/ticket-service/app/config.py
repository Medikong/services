import os


class Settings:
    service_name = "ticket-service"
    database_url = os.getenv("DATABASE_URL", "sqlite:///./ticket_service.db")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    kafka_group_id = os.getenv("KAFKA_GROUP_ID", "ticket-service")

    # Consumer 토픽
    payment_approved_topic = os.getenv("PAYMENT_APPROVED_TOPIC", "payment-approved")

    # Producer 토픽
    ticket_issued_topic = os.getenv("TICKET_ISSUED_TOPIC", "ticket-issued")

    # S3
    aws_access_key_id = os.getenv("AWS_ACCESS_KEY_ID", "")
    aws_secret_access_key = os.getenv("AWS_SECRET_ACCESS_KEY", "")
    aws_region = os.getenv("AWS_REGION", "ap-northeast-2")
    s3_bucket = os.getenv("S3_BUCKET", "ticketing-tickets")


settings = Settings()
