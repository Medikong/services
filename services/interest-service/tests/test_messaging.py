import anyio

from app.messaging import KafkaInterestEventPublisher, NoopInterestEventPublisher, kafka_runtime_from_bootstrap
from app.models import DropId, UserId

DROP_ID = DropId("7d4a8f2c-5e14-46be-9b9b-987f5d69e001")
USER_ID = UserId("user-001")


class FakeProducer:
    """kafka_utils.TraceAwareKafkaProducer와 같은 모양(send_and_wait)만 흉내 낸 테스트 더블.

    실제 aiokafka 브로커/패키지 없이 KafkaInterestEventPublisher가 올바른 topic/key/payload로
    보내는지만 검증한다.
    """

    def __init__(self) -> None:
        self.sent: list[tuple[str, dict, bytes]] = []

    async def send_and_wait(self, topic: str, value: dict, key: bytes) -> None:
        self.sent.append((topic, value, key))


def test_kafka_publisher_sends_interest_added_with_correct_topic_and_key() -> None:
    # Given
    producer = FakeProducer()
    publisher = KafkaInterestEventPublisher(producer)

    # When
    anyio.run(publisher.publish_interest_added, USER_ID, DROP_ID)

    # Then
    assert len(producer.sent) == 1
    topic, payload, key = producer.sent[0]
    assert topic == "interest.added"
    assert key == DROP_ID.encode("utf-8")
    assert payload["userId"] == USER_ID
    assert payload["dropId"] == DROP_ID
    assert payload["producer"] == "interest-service"


def test_kafka_publisher_sends_interest_removed_with_correct_topic() -> None:
    # Given
    producer = FakeProducer()
    publisher = KafkaInterestEventPublisher(producer)

    # When
    anyio.run(publisher.publish_interest_removed, USER_ID, DROP_ID)

    # Then
    topic, payload, key = producer.sent[0]
    assert topic == "interest.removed"
    assert key == DROP_ID.encode("utf-8")


def test_noop_publisher_does_nothing() -> None:
    # Given
    publisher = NoopInterestEventPublisher()

    # When / Then (그냥 예외 없이 끝나야 한다)
    anyio.run(publisher.publish_interest_added, USER_ID, DROP_ID)
    anyio.run(publisher.publish_interest_removed, USER_ID, DROP_ID)


def test_kafka_runtime_from_bootstrap_returns_noop_when_bootstrap_servers_empty() -> None:
    # Given / When
    runtime = kafka_runtime_from_bootstrap("")

    # Then
    assert runtime.publisher is None


def test_kafka_runtime_from_bootstrap_falls_back_to_noop_when_kafka_utils_not_installed() -> None:
    # Given: 이 샌드박스엔 kafka-utils가 선택적 그룹이라 설치돼 있지 않다(pyproject.toml 2026-07-14 수정 이력 참고).
    # When
    runtime = kafka_runtime_from_bootstrap("localhost:9092")

    # Then: ImportError를 삼키고 Noop으로 폴백해야 한다(크래시하면 안 됨).
    assert runtime.publisher is None
