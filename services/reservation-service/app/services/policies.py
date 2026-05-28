from app import entities as model
from app import schemas
from app.services.base import ReservationDomainService, now_utc


class ReservationPolicyService(ReservationDomainService):
    def update_queue_policy(self, concert_id: str, request: schemas.QueuePolicyUpdateRequest) -> schemas.QueuePolicyResponse:
        policy = model.QueuePolicy(
            concert_id=concert_id,
            enabled=request.enabled,
            max_entrants_per_minute=request.maxEntrantsPerMinute,
            waiting_room_url=request.waitingRoomUrl,
        )
        self.policies.save_queue_policy(policy)
        self.sales.get_or_create_sales_state(concert_id, now_utc())
        self.commit()
        return schemas.QueuePolicyResponse(
            concertId=concert_id,
            enabled=request.enabled,
            maxEntrantsPerMinute=request.maxEntrantsPerMinute,
            waitingRoomUrl=request.waitingRoomUrl,
        )

    def update_traffic_policy(self, concert_id: str, request: schemas.TrafficPolicyUpdateRequest) -> schemas.TrafficPolicyResponse:
        policy = model.TrafficPolicy(
            concert_id=concert_id,
            macro_protection_enabled=request.macroProtectionEnabled,
            max_requests_per_user_per_minute=request.maxRequestsPerUserPerMinute,
            block_suspicious_traffic=request.blockSuspiciousTraffic,
        )
        self.policies.save_traffic_policy(policy)
        self.sales.get_or_create_sales_state(concert_id, now_utc())
        self.commit()
        return schemas.TrafficPolicyResponse(
            concertId=concert_id,
            macroProtectionEnabled=request.macroProtectionEnabled,
            maxRequestsPerUserPerMinute=request.maxRequestsPerUserPerMinute,
            blockSuspiciousTraffic=request.blockSuspiciousTraffic,
        )
