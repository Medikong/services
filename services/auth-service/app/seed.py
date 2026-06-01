from sqlalchemy.orm import Session

from app.models import User
from app.security import hash_password


DEMO_USERS = [
    {
        "email": "admin@example.com",
        "password": "admin1234",
        "display_name": "Platform Admin",
        "role": "ADMIN",
    },
    {
        "email": "customer@example.com",
        "password": "customer1234",
        "display_name": "Ticket Customer",
        "role": "CUSTOMER",
    },
    {
        "email": "provider@example.com",
        "password": "provider1234",
        "display_name": "Concert Provider",
        "role": "PROVIDER",
    },
]


def seed_demo_users(db: Session) -> None:
    for account in DEMO_USERS:
        existing = db.query(User).filter(User.email == account["email"]).one_or_none()
        if existing is not None:
            existing.display_name = account["display_name"]
            existing.password_hash = hash_password(account["password"])
            existing.role = account["role"]
            existing.is_active = True
            continue
        db.add(
            User(
                email=account["email"],
                password_hash=hash_password(account["password"]),
                display_name=account["display_name"],
                role=account["role"],
                is_active=True,
            )
        )
    db.commit()
