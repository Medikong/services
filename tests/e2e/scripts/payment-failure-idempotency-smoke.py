from __future__ import annotations

import json
from urllib.error import URLError

import anyio
from aiokafka import AIOKafkaConsumer, AIOKafkaProducer
from contracts import PAYMENT_FAILED_TOPIC
from payment_failure_idempotency_support import (
    KAFKA_DRAIN_TIMEOUT_SECONDS,
    ORDER_POLL_INTERVAL_SECONDS,
    PAYMENT_READY_RETRY_INTERVAL_SECONDS,
    CapturedPaymentFailed,
    ExpectedPaymentFailed,
    OrderData,
    OrderResponse,
    PaymentData,
    ScenarioConfig,
    SmokeFailure,
    SmokeOutput,
    captured_event,
    config_from_env,
    failure_output,
    is_payment_not_ready_conflict,
    payment_from_result,
    post_order,
    post_payment_failure,
    select_order,
)


async def create_order(config: ScenarioConfig) -> OrderData:
    result = await anyio.to_thread.run_sync(post_order, config)
    if result.status != 201:
        raise SmokeFailure(f"order create returned {result.status}: {result.body!r}")
    order = OrderResponse.model_validate_json(result.body).data
    if order.status != "PENDING_PAYMENT":
        raise SmokeFailure(f"order status mismatch after create: {order.status}")
    return order


async def fail_payment_once(config: ScenarioConfig, order: OrderData) -> PaymentData:
    result = await anyio.to_thread.run_sync(post_payment_failure, config, order)
    return payment_from_result(result)


async def fail_payment_when_ready(
    config: ScenarioConfig,
    order: OrderData,
) -> PaymentData:
    deadline = anyio.current_time() + config.timeout_seconds
    result = await anyio.to_thread.run_sync(post_payment_failure, config, order)
    while is_payment_not_ready_conflict(result):
        if anyio.current_time() >= deadline:
            break
        await anyio.sleep(PAYMENT_READY_RETRY_INTERVAL_SECONDS)
        result = await anyio.to_thread.run_sync(post_payment_failure, config, order)
    return payment_from_result(result)


async def collect_payment_failed(
    consumer: AIOKafkaConsumer,
    expected: ExpectedPaymentFailed,
    captured: list[CapturedPaymentFailed],
    target_count: int,
    timeout_seconds: int,
) -> None:
    with anyio.fail_after(timeout_seconds):
        while len(captured) < target_count:
            batches = await consumer.getmany(timeout_ms=500)
            for messages in batches.values():
                for message in messages:
                    event = captured_event(message, expected)
                    if event is not None:
                        captured.append(event)


async def assert_no_extra_payment_failed(
    consumer: AIOKafkaConsumer,
    expected: ExpectedPaymentFailed,
) -> None:
    with anyio.move_on_after(KAFKA_DRAIN_TIMEOUT_SECONDS):
        while True:
            batches = await consumer.getmany(timeout_ms=500)
            for messages in batches.values():
                for message in messages:
                    if captured_event(message, expected) is not None:
                        raise SmokeFailure(
                            "received more than three matching payment.failed copies",
                        )


async def poll_order_failed(
    config: ScenarioConfig,
    order_id: str,
    payment_id: str,
) -> OrderData:
    deadline = anyio.current_time() + config.timeout_seconds
    while anyio.current_time() < deadline:
        result = await anyio.to_thread.run_sync(select_order, config, order_id)
        if result.status == 200:
            order = OrderResponse.model_validate_json(result.body).data
            if order.status == "PAYMENT_FAILED" and order.paymentId == payment_id:
                return order
        await anyio.sleep(ORDER_POLL_INTERVAL_SECONDS)
    raise SmokeFailure(
        f"order {order_id} did not reach PAYMENT_FAILED with payment {payment_id}",
    )


async def run_smoke(config: ScenarioConfig) -> SmokeOutput:
    consumer = AIOKafkaConsumer(
        PAYMENT_FAILED_TOPIC,
        bootstrap_servers=config.bootstrap_servers,
        group_id=f"{config.ids.run_id}-payment-failure-smoke",
        auto_offset_reset="latest",
        enable_auto_commit=False,
    )
    producer = AIOKafkaProducer(bootstrap_servers=config.bootstrap_servers)
    captured: list[CapturedPaymentFailed] = []

    await consumer.start()
    await producer.start()
    try:
        await consumer.getmany(timeout_ms=1000)
        order = await create_order(config)
        first_payment = await fail_payment_when_ready(config, order)
        second_payment = await fail_payment_once(config, order)
        payment_ids_match = first_payment.id == second_payment.id
        if not payment_ids_match:
            raise SmokeFailure(
                f"duplicate failure returned different payment ids: "
                f"{first_payment.id} != {second_payment.id}",
            )

        expected = ExpectedPaymentFailed(
            user_id=config.ids.user_id,
            order_id=order.id,
            payment_id=first_payment.id,
        )
        await collect_payment_failed(consumer, expected, captured, 1, config.timeout_seconds)
        original = captured[0]
        for _ in range(2):
            await producer.send_and_wait(
                PAYMENT_FAILED_TOPIC,
                original.value,
                key=original.key,
            )
        await collect_payment_failed(consumer, expected, captured, 3, config.timeout_seconds)
        await assert_no_extra_payment_failed(consumer, expected)
        failed_order = await poll_order_failed(config, order.id, first_payment.id)

        event_ids = [event.event.eventId for event in captured]
        unique_event_ids = sorted(set(event_ids))
        ok = len(captured) == 3 and len(unique_event_ids) == 1
        if not ok:
            raise SmokeFailure(
                f"expected 3 payment.failed copies with 1 eventId, got "
                f"{len(captured)} copies and {len(unique_event_ids)} eventIds",
            )
        return {
            "captured_payment_failed_copies": len(captured),
            "event_ids": event_ids,
            "ok": True,
            "order_id": order.id,
            "order_status": failed_order.status,
            "payment_id": first_payment.id,
            "payment_ids_match": payment_ids_match,
            "run_id": config.ids.run_id,
            "unique_event_ids": unique_event_ids,
            "user_id": config.ids.user_id,
        }
    finally:
        await producer.stop()
        await consumer.stop()


def main() -> int:
    config = config_from_env()
    try:
        output = anyio.run(run_smoke, config)
    except (SmokeFailure, TimeoutError, URLError) as exc:
        output = failure_output(config, f"{type(exc).__name__}: {exc}")
    print(json.dumps(output, sort_keys=True, separators=(",", ":")))
    return 0 if output["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
