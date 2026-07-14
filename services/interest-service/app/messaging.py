from dataclasses import dataclass
from typing import TYPE_CHECKING, Protocol

from app.events import (
    INTEREST_ADDED_TOPIC,
    INTEREST_REMOVED_TOPIC,
    interest_added_event,
    interest_removed_event,
)
from app.models import DropId, UserId

if TYPE_CHECKING:
    from kafka_utils import TraceAwareKafkaProducer


class InterestEventPublisher(Protocol):
    async def publish_interest_added(self, user_id: UserId, drop_id: DropId) -> None: ...

    async def publish_interest_removed(self, user_id: UserId, drop_id: DropId) -> None: ...


class NoopInterestEventPublisher:
    async def publish_interest_added(self, user_id: UserId, drop_id: DropId) -> None:
        return None

    async def publish_interest_removed(self, user_id: UserId, drop_id: DropId) -> None:
        return None


class KafkaInterestEventPublisher:
    def __init__(self, producer: "TraceAwareKafkaProducer") -> None:
        self._producer = producer

    async def start(self) -> None:
        await self._producer.start()

    async def stop(self) -> None:
        await self._producer.stop()

    async def publish_interest_added(self, user_id: UserId, drop_id: DropId) -> None:
        event = interest_added_event(user_id, drop_id)
        await self._producer.send_and_wait(
            INTEREST_ADDED_TOPIC,
            event.model_dump(mode="json"),
            key=drop_id.encode("utf-8"),
        )

    async def publish_interest_removed(self, user_id: UserId, drop_id: DropId) -> None:
        event = interest_removed_event(user_id, drop_id)
        await self._producer.send_and_wait(
            INTEREST_REMOVED_TOPIC,
            event.model_dump(mode="json"),
            key=drop_id.encode("utf-8"),
        )


@dataclass(frozen=True, slots=True)
class KafkaRuntime:
    publisher: KafkaInterestEventPublisher | None


def kafka_runtime_from_bootstrap(bootstrap_servers: str) -> KafkaRuntime:
    """`kafka_utils`(→ `aiokafka`)는 선택적 의존성 그룹(`kafka`)이라 기본으로는 설치돼 있지 않다.

    `KAFKA_BOOTSTRAP_SERVERS`가 비어 있으면(로컬/테스트 기본값) import조차 시도하지 않아,
    팀원이 카프카 없이 이 서비스를 쓸 때 aiokafka 빌드가 전혀 필요 없다.
    """
    if bootstrap_servers == "":
        return KafkaRuntime(publisher=None)

    try:
        from kafka_utils import create_kafka_producer
    except ImportError:
        return KafkaRuntime(publisher=None)

    producer = create_kafka_producer(bootstrap_servers, client_id="interest-service")
    if producer is None:
        return KafkaRuntime(publisher=None)

    return KafkaRuntime(publisher=KafkaInterestEventPublisher(producer))
