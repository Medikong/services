import os

from contracts.events import (
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    RESERVATION_CREATED_TOPIC,
    RESERVATION_EXPIRED_TOPIC,
    TICKET_ISSUED_TOPIC,
)


class Settings:
    service_name = "notification-service"
    mongodb_url = os.getenv("MONGODB_URL", "mongodb://localhost:27017")
    mongodb_db_name = os.getenv("MONGODB_DB_NAME", "notification_db")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    kafka_group_id = os.getenv("KAFKA_GROUP_ID", "notification-service")

    # Kafka 토픽
    reservation_created_topic = os.getenv("RESERVATION_CREATED_TOPIC", RESERVATION_CREATED_TOPIC)
    reservation_expired_topic = os.getenv("RESERVATION_EXPIRED_TOPIC", RESERVATION_EXPIRED_TOPIC)
    payment_approved_topic = os.getenv("PAYMENT_APPROVED_TOPIC", PAYMENT_APPROVED_TOPIC)
    payment_failed_topic = os.getenv("PAYMENT_FAILED_TOPIC", PAYMENT_FAILED_TOPIC)
    ticket_issued_topic = os.getenv("TICKET_ISSUED_TOPIC", TICKET_ISSUED_TOPIC)


settings = Settings()
