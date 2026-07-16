#!/usr/bin/env python3
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "scenarios" / "auth" / "auth.postman_collection.json"


def script_lines(source):
    return [line.rstrip() for line in source.strip().splitlines()]


def request(name, method, url, *, body=None, headers=None, tests=""):
    header_items = [{"key": key, "value": value, "type": "text"} for key, value in (headers or [])]
    if body is not None and not any(key.lower() == "content-type" for key, _ in (headers or [])):
        header_items.append({"key": "Content-Type", "value": "application/json", "type": "text"})
    value = {
        "name": name,
        "request": {"method": method, "header": header_items, "url": url},
        "response": [],
    }
    if body is not None:
        value["request"]["body"] = {
            "mode": "raw",
            "raw": json.dumps(body, ensure_ascii=False, separators=(",", ":")),
            "options": {"raw": {"language": "json"}},
        }
    if tests:
        value["event"] = [{"listen": "test", "script": {"type": "text/javascript", "exec": script_lines(tests)}}]
    return value


def status_test(code, extra=""):
    return f"""
let statusPayload = {{}};
if (pm.response.code !== 204) {{ try {{ statusPayload = pm.response.json(); }} catch (_) {{}} }}
const statusCode = statusPayload.code ? " " + statusPayload.code : "";
pm.test("HTTP {code}" + statusCode, function () {{ pm.response.to.have.status({code}); }});
if (pm.response.code === {code}) {{
{extra}
}}
"""


def admin_headers():
    return [("Authorization", "Bearer {{adminToken}}")]


def bearer(variable="accessToken"):
    return [("Authorization", f"Bearer {{{{{variable}}}}}")]


def flow_headers(prefix):
    return [("X-Auth-Flow-Token", f"{{{{{prefix}FlowToken}}}}"), ("Idempotency-Key", "{{$randomUUID}}")]


def create_intent(prefix, name=None):
    return request(
        name or f"{prefix} create intent",
        "POST",
        "{{gatewayUrl}}/api/v1/auth/intents",
        headers=[("X-Client-Channel", "ios"), ("Idempotency-Key", "{{$randomUUID}}")],
        body={"returnPath": "/e2e", "intentType": "navigation"},
        tests=status_test(201, f"""
const body = (pm.response.json().data || pm.response.json());
pm.collectionVariables.set("{prefix}IntentId", body.authIntentId);
pm.collectionVariables.set("{prefix}FlowToken", body.authFlowToken);
"""),
    )


def email_signin(prefix, *, password="{{currentPassword}}", expect=200, save_main=True):
    items = [create_intent(prefix)]
    tests = status_test(expect)
    if expect == 200:
        tests = status_test(200, f"""
const body = (pm.response.json().data || pm.response.json());
pm.collectionVariables.set("{prefix}AccessToken", body.tokens.accessToken);
pm.collectionVariables.set("{prefix}RefreshToken", body.tokens.refreshToken);
pm.collectionVariables.set("{prefix}SessionId", body.session.sessionId);
{"pm.collectionVariables.set(\"accessToken\", body.tokens.accessToken); pm.collectionVariables.set(\"refreshToken\", body.tokens.refreshToken); pm.collectionVariables.set(\"sessionId\", body.session.sessionId);" if save_main else ""}
""")
    items.append(request(
        f"{prefix} email signin",
        "POST",
        "{{gatewayUrl}}/api/v1/auth/signins/email",
        headers=flow_headers(prefix),
        body={"authIntentId": f"{{{{{prefix}IntentId}}}}", "email": "{{email}}", "password": password, "rememberMe": False},
        tests=tests,
    ))
    return items


def provider_poll(name, channel, destination, code_variable, *, min_attempts=0):
    counter = code_variable + "Polls"
    extra = ""
    if min_attempts:
        extra = f'pm.test("provider retried", function () {{ pm.expect(body.attempts || {min_attempts}).to.be.at.least({min_attempts}); }});'
    return request(
        name,
        "POST",
        "{{providerUrl}}/admin/provider/latest",
        headers=admin_headers(),
        body={"channel": channel, "destination": destination},
        tests=f"""
function retry() {{
  const count = Number(pm.collectionVariables.get("{counter}") || 0) + 1;
  pm.collectionVariables.set("{counter}", count);
  if (count < 16) {{ pm.execution.setNextRequest(pm.info.requestName); return; }}
  pm.test("provider delivery completed", function () {{ pm.expect.fail("provider delivery did not complete"); }});
}}
if (pm.response.code !== 200) {{ retry(); }} else {{
  const body = (pm.response.json().data || pm.response.json());
  if (!body.accepted) {{ retry(); }} else {{
    pm.collectionVariables.unset("{counter}");
    pm.collectionVariables.set("{code_variable}", body.code);
    pm.test("provider accepted one delivery", function () {{ pm.expect(body.code).to.match(/^\\d{{6}}$/); }});
    {extra}
  }}
}}
""",
    )


def phone_signin(prefix, phone_national="{{phoneNational}}", phone_encoded="{{phoneEncoded}}", *, save_main=True):
    items = [create_intent(prefix)]
    items.append(request(
        f"{prefix} issue phone signin",
        "POST",
        "{{gatewayUrl}}/api/v1/auth/signins/phone/challenges",
        headers=flow_headers(prefix),
        body={"authIntentId": f"{{{{{prefix}IntentId}}}}", "phone": {"countryCode": "+82", "nationalNumber": phone_national}, "rememberMe": False},
        tests=status_test(202, f'pm.collectionVariables.set("{prefix}ChallengeId", (pm.response.json().data || pm.response.json()).challengeId);'),
    ))
    items.append(provider_poll(f"{prefix} wait for SMS", "sms", phone_encoded, f"{prefix}Code"))
    save = f"""
const body = (pm.response.json().data || pm.response.json());
pm.collectionVariables.set("{prefix}AccessToken", body.tokens.accessToken);
pm.collectionVariables.set("{prefix}RefreshToken", body.tokens.refreshToken);
pm.collectionVariables.set("{prefix}SessionId", body.session.sessionId);
{"pm.collectionVariables.set(\"accessToken\", body.tokens.accessToken); pm.collectionVariables.set(\"refreshToken\", body.tokens.refreshToken); pm.collectionVariables.set(\"sessionId\", body.session.sessionId);" if save_main else ""}
"""
    items.append(request(
        f"{prefix} verify phone signin",
        "POST",
        f"{{{{gatewayUrl}}}}/api/v1/auth/signins/phone/challenges/{{{{{prefix}ChallengeId}}}}/verify",
        headers=flow_headers(prefix),
        body={"code": f"{{{{{prefix}Code}}}}"},
        tests=status_test(200, save),
    ))
    return items


def startup_folder():
    return {"name": "startup-readiness", "item": [
        request("gateway liveness", "GET", "{{gatewayUrl}}/healthz", tests=status_test(200)),
        request("auth readiness", "GET", "{{gatewayUrl}}/e2e/auth-readyz", tests=status_test(200)),
        request("worker readiness", "GET", "{{gatewayUrl}}/e2e/worker-readyz", tests=status_test(200)),
        request("development route is absent", "GET", "{{gatewayUrl}}/api/v1/dev/auth/verification-messages/{{$randomUUID}}", tests=status_test(404)),
    ]}


def registration_folder():
    items = [
        request("reset mock providers", "POST", "{{providerUrl}}/admin/provider/reset", headers=admin_headers(), body={}, tests=status_test(200)),
        create_intent("registration"),
        request(
            "start registration", "POST", "{{gatewayUrl}}/api/v1/auth/registrations",
            headers=flow_headers("registration"),
            body={
                "authIntentId": "{{registrationIntentId}}", "email": "{{email}}", "password": "{{currentPassword}}",
                "phone": {"countryCode": "+82", "nationalNumber": "{{phoneNational}}"},
                "profileRequestId": "{{$randomUUID}}", "agreementReceiptId": "{{$randomUUID}}", "rememberMe": False,
            },
            tests=status_test(201, 'pm.collectionVariables.set("registrationId", (pm.response.json().data || pm.response.json()).registrationId);'),
        ),
        request(
            "issue registration email", "POST", "{{gatewayUrl}}/api/v1/auth/registrations/{{registrationId}}/challenges",
            headers=flow_headers("registration"), body={"method": "email"},
            tests=status_test(201, 'pm.collectionVariables.set("registrationEmailChallenge", (pm.response.json().data || pm.response.json()).challengeId);'),
        ),
        provider_poll("wait for registration email", "email", "{{email}}", "registrationEmailCode"),
        request(
            "verify registration email", "POST", "{{gatewayUrl}}/api/v1/auth/registrations/{{registrationId}}/challenges/{{registrationEmailChallenge}}/verify",
            headers=flow_headers("registration"), body={"code": "{{registrationEmailCode}}"}, tests=status_test(200),
        ),
        request(
            "issue registration SMS", "POST", "{{gatewayUrl}}/api/v1/auth/registrations/{{registrationId}}/challenges",
            headers=flow_headers("registration"), body={"method": "phone"},
            tests=status_test(201, 'pm.collectionVariables.set("registrationPhoneChallenge", (pm.response.json().data || pm.response.json()).challengeId);'),
        ),
        provider_poll("wait for registration SMS", "sms", "{{phoneEncoded}}", "registrationPhoneCode"),
        request(
            "verify registration SMS", "POST", "{{gatewayUrl}}/api/v1/auth/registrations/{{registrationId}}/challenges/{{registrationPhoneChallenge}}/verify",
            headers=flow_headers("registration"), body={"code": "{{registrationPhoneCode}}"}, tests=status_test(200),
        ),
        request(
            "create signed User proof", "POST", "{{providerUrl}}/admin/proofs/user-creation", headers=admin_headers(),
            body={"registrationId": "{{registrationId}}", "userId": "{{userId}}"},
            tests=status_test(200, 'pm.collectionVariables.set("userCreationProof", (pm.response.json().data || pm.response.json()).proof);'),
        ),
        request(
            "complete registration", "POST", "{{gatewayUrl}}/api/v1/auth/registrations/{{registrationId}}/complete",
            headers=flow_headers("registration"), body={"userId": "{{userId}}", "userCreationProof": "{{userCreationProof}}"},
            tests=status_test(200, """
const body = (pm.response.json().data || pm.response.json());
pm.collectionVariables.set("accessToken", body.tokens.accessToken);
pm.collectionVariables.set("refreshToken", body.tokens.refreshToken);
pm.collectionVariables.set("sessionId", body.session.sessionId);
pm.test("registration issued mobile credentials", function () { pm.expect(body.credentialDelivery).to.eql("mobile_tokens"); });
"""),
        ),
        request(
            "registration delivery state", "GET", "{{controlUrl}}/admin/state/delivery", headers=admin_headers(),
            tests=status_test(200, 'const counts=(pm.response.json().data || pm.response.json()).counts; pm.test("both deliveries completed", function(){pm.expect(counts.delivered).to.be.at.least(2);});'),
        ),
    ]
    return {"name": "registration-email-sms", "item": items}


def signin_folder():
    items = email_signin("emailSignin")
    items += phone_signin("phoneSignin")
    items.append(request("phone signin context", "GET", "{{gatewayUrl}}/api/v1/auth/context", headers=bearer(), tests=status_test(200, 'pm.test("same user", function(){pm.expect((pm.response.json().data || pm.response.json()).userId).to.eql(pm.collectionVariables.get("userId"));});')))
    return {"name": "email-phone-signin", "item": items}


def gateway_folder():
    items = email_signin("gatewaySignin")
    items += [
        request("JWKS exposes RSA key", "GET", "{{gatewayUrl}}/.well-known/jwks.json", tests=status_test(200, 'const key=(pm.response.json().data || pm.response.json()).keys[0]; pm.test("RS256 JWKS", function(){pm.expect(key.kty).to.eql("RSA"); pm.expect(key.alg).to.eql("RS256");});')),
        request("read echo count before invalid JWT", "GET", "{{controlUrl}}/admin/state/echo", headers=admin_headers(), tests=status_test(200, 'pm.collectionVariables.set("echoBeforeInvalid", (pm.response.json().data || pm.response.json()).count);')),
        request("invalid JWT is rejected", "GET", "{{gatewayUrl}}/protected", headers=[("Authorization", "Bearer invalid.jwt.value")], tests=status_test(401)),
        request("invalid JWT never reaches echo", "GET", "{{controlUrl}}/admin/state/echo", headers=admin_headers(), tests=status_test(200, 'pm.test("echo untouched", function(){pm.expect((pm.response.json().data || pm.response.json()).count).to.eql(Number(pm.collectionVariables.get("echoBeforeInvalid")));});')),
        request(
            "gateway replaces internal headers", "GET", "{{gatewayUrl}}/protected",
            headers=bearer() + [("X-User-Id", "external-user"), ("X-Session-Id", "external-session"), ("X-Token-Id", "external-token"), ("X-User-Role", "external-role")],
            tests=status_test(200, """
const body=(pm.response.json().data || pm.response.json());
const token=pm.collectionVariables.get("accessToken");
let part=token.split('.')[1].replace(/-/g,'+').replace(/_/g,'/'); while(part.length%4){part+='=';}
const claims=JSON.parse(require('atob')(part));
pm.test("sub sid jti become the only identity headers", function(){
  pm.expect(body.headers.userId).to.eql(claims.sub);
  pm.expect(body.headers.sessionId).to.eql(claims.sid);
  pm.expect(body.headers.tokenId).to.eql(claims.jti);
  pm.expect(body.headers.userId).not.to.eql("external-user");
});
"""),
        ),
        request("edge cannot bypass gateway", "GET", "{{edgeProbeUrl}}/admin/assert/protected-bypass", headers=admin_headers(), tests=status_test(200, 'pm.test("network boundary blocked direct call", function(){pm.expect((pm.response.json().data || pm.response.json()).blocked).to.eql(true);});')),
    ]
    return {"name": "jwks-jwt-gateway", "item": items}


def session_folder():
    items = email_signin("rotationSignin")
    items += [
        request("remember original refresh", "GET", "{{gatewayUrl}}/healthz", tests=status_test(200, 'pm.collectionVariables.set("originalRefreshToken", pm.collectionVariables.get("refreshToken"));')),
        request(
            "rotate refresh token", "POST", "{{gatewayUrl}}/api/v1/auth/sessions/refresh",
            headers=[("X-Refresh-Token", "{{originalRefreshToken}}"), ("Idempotency-Key", "{{$randomUUID}}")], body={},
            tests=status_test(200, 'const body=(pm.response.json().data || pm.response.json()); pm.collectionVariables.set("accessToken",body.tokens.accessToken); pm.collectionVariables.set("refreshToken",body.tokens.refreshToken); pm.collectionVariables.set("sessionId",body.session.sessionId);'),
        ),
        request("reuse old refresh token", "POST", "{{gatewayUrl}}/api/v1/auth/sessions/refresh", headers=[("X-Refresh-Token", "{{originalRefreshToken}}"), ("Idempotency-Key", "{{$randomUUID}}")], body={}, tests=status_test(401)),
        request("reused family access is rejected", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(401)),
        request("reused family is terminal in PostgreSQL", "GET", "{{controlUrl}}/admin/state/session?sessionId={{sessionId}}", headers=admin_headers(), tests=status_test(200, 'pm.test("reuse is durably detected", function(){pm.expect((pm.response.json().data || pm.response.json()).status).to.eql("reuse_detected");});')),
    ]
    items += email_signin("logoutSignin")
    items += [
        request("logout by refresh token", "POST", "{{gatewayUrl}}/api/v1/auth/sessions/logout", headers=[("X-Refresh-Token", "{{refreshToken}}"), ("Idempotency-Key", "{{$randomUUID}}")], body={}, tests=status_test(204)),
        request("logged out access is rejected", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(401)),
        request("logout is durable", "GET", "{{controlUrl}}/admin/state/session?sessionId={{sessionId}}", headers=admin_headers(), tests=status_test(200, 'pm.test("logout persisted", function(){pm.expect((pm.response.json().data || pm.response.json()).status).to.eql("revoked");});')),
    ]
    return {"name": "refresh-rotation-logout", "item": items}


def password_reset_folder():
    items = email_signin("resetExisting")
    items.append(request("save session before password reset", "GET", "{{gatewayUrl}}/healthz", tests=status_test(200, 'pm.collectionVariables.set("preResetAccess",pm.collectionVariables.get("accessToken")); pm.collectionVariables.set("preResetSession",pm.collectionVariables.get("sessionId"));')))
    items += [create_intent("passwordReset")]
    items += [
        request("start password reset", "POST", "{{gatewayUrl}}/api/v1/auth/password-resets", headers=flow_headers("passwordReset"), body={"identifierType": "email", "email": "{{email}}"}, tests=status_test(202, 'pm.collectionVariables.set("passwordResetId",(pm.response.json().data || pm.response.json()).passwordResetId);')),
        request("issue password reset email", "POST", "{{gatewayUrl}}/api/v1/auth/password-resets/{{passwordResetId}}/challenges", headers=flow_headers("passwordReset"), body={"method": "email"}, tests=status_test(202, 'pm.collectionVariables.set("passwordResetChallenge",(pm.response.json().data || pm.response.json()).challengeId);')),
        provider_poll("wait for password reset email", "email", "{{email}}", "passwordResetCode"),
        request("verify password reset email", "POST", "{{gatewayUrl}}/api/v1/auth/password-resets/{{passwordResetId}}/challenges/{{passwordResetChallenge}}/verify", headers=flow_headers("passwordReset"), body={"code": "{{passwordResetCode}}"}, tests=status_test(200, 'pm.collectionVariables.set("resetGrant",(pm.response.json().data || pm.response.json()).resetGrant);')),
        request("complete password reset", "PUT", "{{gatewayUrl}}/api/v1/auth/password-resets/{{passwordResetId}}/password", headers=flow_headers("passwordReset"), body={"credentialDelivery": "mobile_reset_grant", "resetGrant": "{{resetGrant}}", "newPassword": "{{newPassword}}", "confirmPassword": "{{newPassword}}"}, tests=status_test(204, 'pm.collectionVariables.set("currentPassword",pm.collectionVariables.get("newPassword"));')),
        request("password reset revokes access", "GET", "{{gatewayUrl}}/protected", headers=bearer("preResetAccess"), tests=status_test(401)),
        request("password reset revokes PostgreSQL session", "GET", "{{controlUrl}}/admin/state/session?sessionId={{preResetSession}}", headers=admin_headers(), tests=status_test(200, 'pm.test("all prior sessions revoked",function(){pm.expect((pm.response.json().data || pm.response.json()).status).to.eql("revoked");});')),
    ]
    items += email_signin("oldPassword", password="{{initialPassword}}", expect=401, save_main=False)
    items += email_signin("newPasswordSignin", password="{{currentPassword}}", expect=200)
    return {"name": "password-reset-revocation", "item": items}


def identity_folder():
    items = email_signin("identitySignin")
    items += [
        request("reauthenticate for identity link", "POST", "{{gatewayUrl}}/api/v1/auth/reauthentications/email", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}")], body={"purpose": "link_identity", "password": "{{currentPassword}}"}, tests=status_test(200, 'const body=(pm.response.json().data || pm.response.json()); pm.collectionVariables.set("linkProof",body.reauthenticationProof); pm.collectionVariables.set("accessToken",body.tokens.accessToken); pm.collectionVariables.set("refreshToken",body.tokens.refreshToken);')),
        request("existing authentication method link is stable", "POST", "{{gatewayUrl}}/api/v1/auth/method-links", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}")], body={"method": "phone", "destination": {"countryCode": "+82", "nationalNumber": "{{phoneNational}}"}, "reauthenticationProof": "{{linkProof}}"}, tests=status_test(200, 'pm.test("existing link remains active",function(){const body=(pm.response.json().data || pm.response.json()); pm.expect(body.status).to.eql("active"); pm.expect(body.method).to.eql("phone");});')),
        request("reauthenticate for phone replacement", "POST", "{{gatewayUrl}}/api/v1/auth/reauthentications/email", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}")], body={"purpose": "replace_phone", "password": "{{currentPassword}}"}, tests=status_test(200, 'const body=(pm.response.json().data || pm.response.json()); pm.collectionVariables.set("replaceProof",body.reauthenticationProof); pm.collectionVariables.set("accessToken",body.tokens.accessToken); pm.collectionVariables.set("refreshToken",body.tokens.refreshToken);')),
        request("start phone replacement", "POST", "{{gatewayUrl}}/api/v1/auth/phone-replacements", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}")], body={"newPhone": {"countryCode": "+82", "nationalNumber": "{{replacementPhoneNational}}"}, "reauthenticationProof": "{{replaceProof}}"}, tests=status_test(201, 'pm.collectionVariables.set("replacementId",(pm.response.json().data || pm.response.json()).replacementId);')),
        request("issue phone replacement SMS", "POST", "{{gatewayUrl}}/api/v1/auth/phone-replacements/{{replacementId}}/challenges", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}")], body={}, tests=status_test(201, 'pm.collectionVariables.set("replacementChallenge",(pm.response.json().data || pm.response.json()).challengeId);')),
        provider_poll("wait for phone replacement SMS", "sms", "{{replacementPhoneEncoded}}", "replacementPhoneCode"),
        request("complete phone replacement", "POST", "{{gatewayUrl}}/api/v1/auth/phone-replacements/{{replacementId}}/complete", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}")], body={"challengeId": "{{replacementChallenge}}", "proof": {"type": "code", "value": "{{replacementPhoneCode}}"}}, tests=status_test(200, 'const body=(pm.response.json().data || pm.response.json()); pm.collectionVariables.set("accessToken",body.tokens.accessToken); pm.collectionVariables.set("refreshToken",body.tokens.refreshToken); pm.collectionVariables.set("sessionId",body.session.sessionId); pm.collectionVariables.set("phoneNational",pm.collectionVariables.get("replacementPhoneNational")); pm.collectionVariables.set("phoneEncoded",pm.collectionVariables.get("replacementPhoneEncoded"));')),
    ]
    return {"name": "identity-link-phone-replacement", "item": items}


def session_timeout_folder():
    items = email_signin("timeoutSignin")
    items += [
        request("prime Session cache", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
        request("record echo before dependency failure", "GET", "{{controlUrl}}/admin/state/echo", headers=admin_headers(), tests=status_test(200, 'pm.collectionVariables.set("echoBeforeTimeout",(pm.response.json().data || pm.response.json()).count);')),
        request("pause Redis", "POST", "{{controlUrl}}/admin/containers/redis/pause", headers=admin_headers(), body={}, tests=status_test(200)),
        request("Session check fails closed", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(503, 'pm.test("gateway timeout is bounded",function(){pm.expect(pm.response.responseTime).to.be.below(1200);});')),
        request("resume Redis", "POST", "{{controlUrl}}/admin/containers/redis/unpause", headers=admin_headers(), body={}, tests=status_test(200)),
        request("failed request never reached echo", "GET", "{{controlUrl}}/admin/state/echo", headers=admin_headers(), tests=status_test(200, 'pm.test("echo untouched",function(){pm.expect((pm.response.json().data || pm.response.json()).count).to.eql(Number(pm.collectionVariables.get("echoBeforeTimeout")));});')),
        request("Session check recovers", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
    ]
    return {"name": "session-timeout-fail-closed", "item": items}


def redis_fallback_folder():
    items = email_signin("redisSignin")
    items += [
        request("Redis miss falls back to PostgreSQL", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
        request("fallback writes Session cache", "GET", "{{controlUrl}}/admin/redis/status?sessionId={{sessionId}}", headers=admin_headers(), tests=status_test(200, 'pm.test("cache hit is available",function(){pm.expect((pm.response.json().data || pm.response.json()).exists).to.eql(true);});')),
        request("delete Session cache", "POST", "{{controlUrl}}/admin/redis/delete", headers=admin_headers(), body={"sessionId": "{{sessionId}}"}, tests=status_test(200)),
        request("PostgreSQL fallback succeeds again", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
        request("delete cache before write failure", "POST", "{{controlUrl}}/admin/redis/delete", headers=admin_headers(), body={"sessionId": "{{sessionId}}"}, tests=status_test(200)),
        request("pause Redis writes", "POST", "{{controlUrl}}/admin/redis/pause-writes", headers=admin_headers(), body={"delayMillis": 800}, tests=status_test(200)),
        request("write-through failure returns 503", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(503)),
        request("failed write did not create cache", "GET", "{{controlUrl}}/admin/redis/status?sessionId={{sessionId}}", headers=admin_headers(), tests=status_test(200, 'pm.test("cache remains empty",function(){pm.expect((pm.response.json().data || pm.response.json()).exists).to.eql(false);});')),
        request("write-through recovers", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests="""
if(pm.response.code===200){pm.collectionVariables.unset("redisRecoveryPolls");pm.test("HTTP 200",function(){pm.response.to.have.status(200);});}
else{const count=Number(pm.collectionVariables.get("redisRecoveryPolls")||0)+1;pm.collectionVariables.set("redisRecoveryPolls",count);if(pm.response.code===503&&count<10){pm.execution.setNextRequest(pm.info.requestName);}else{pm.test("Redis write-through recovered",function(){pm.expect.fail("Redis write-through did not recover");});}}
"""),
        request("recovery recreates cache", "GET", "{{controlUrl}}/admin/redis/status?sessionId={{sessionId}}", headers=admin_headers(), tests=status_test(200, 'pm.test("cache restored",function(){pm.expect((pm.response.json().data || pm.response.json()).exists).to.eql(true);});')),
    ]
    return {"name": "redis-fallback-write-through", "item": items}


def provider_fault_group(label, status, delay):
    prefix = "provider" + label.title()
    items = [
        request(f"configure {label} Provider fault", "POST", "{{providerUrl}}/admin/provider/fault", headers=admin_headers(), body={"channel": "sms", "status": status, "failures": 1, "delayMillis": delay}, tests=status_test(200)),
        create_intent(prefix),
        request(f"issue SMS during {label}", "POST", "{{gatewayUrl}}/api/v1/auth/signins/phone/challenges", headers=flow_headers(prefix), body={"authIntentId": f"{{{{{prefix}IntentId}}}}", "phone": {"countryCode": "+82", "nationalNumber": "{{phoneNational}}"}, "rememberMe": False}, tests=status_test(202, f'pm.collectionVariables.set("{prefix}Challenge",(pm.response.json().data || pm.response.json()).challengeId);')),
        provider_poll(f"wait for {label} retry", "sms", "{{phoneEncoded}}", f"{prefix}Code"),
        request(f"assert {label} retry count", "GET", "{{providerUrl}}/admin/provider/stats", headers=admin_headers(), tests=status_test(200, 'pm.test("Provider retried",function(){pm.expect((pm.response.json().data || pm.response.json()).attempts.sms).to.be.at.least(2);});')),
        request(f"reset after {label}", "POST", "{{providerUrl}}/admin/provider/reset", headers=admin_headers(), body={}, tests=status_test(200)),
    ]
    return items


def provider_folder():
    items = []
    items += provider_fault_group("timeout", 0, 800)
    items += provider_fault_group("rate-limit", 429, 0)
    items += provider_fault_group("server-error", 500, 0)
    items.append(request("Provider retry rows are delivered", "GET", "{{controlUrl}}/admin/state/delivery", headers=admin_headers(), tests=status_test(200, 'pm.test("delivery rows recovered",function(){pm.expect((pm.response.json().data || pm.response.json()).counts.delivered).to.be.at.least(3);});')))
    return {"name": "provider-retry", "item": items}


def outbox_folder():
    items = email_signin("outboxSignin")
    items += [
        request("stop Kafka", "POST", "{{controlUrl}}/admin/containers/kafka/stop", headers=admin_headers(), body={}, tests=status_test(200)),
        create_intent("outboxEvent", "create outbox correlation while Kafka is down"),
        request("create outbox event while Kafka is down", "POST", "{{gatewayUrl}}/api/v1/auth/signins/phone/challenges", headers=flow_headers("outboxEvent"), body={"authIntentId": "{{outboxEventIntentId}}", "phone": {"countryCode": "+82", "nationalNumber": "{{phoneNational}}"}, "rememberMe": False}, tests=status_test(202)),
        request("outbox remains unpublished without broker ack", "GET", "{{controlUrl}}/admin/state/outbox/latest?correlationId={{outboxEventIntentId}}", headers=admin_headers(), tests=status_test(200, 'const body=(pm.response.json().data || pm.response.json()); pm.collectionVariables.set("outboxEventId",body.eventId); pm.test("event is not published",function(){pm.expect(body.status).not.to.eql("published");});')),
        request("start Kafka", "POST", "{{controlUrl}}/admin/containers/kafka/start", headers=admin_headers(), body={}, tests=status_test(200)),
        request("wait for worker broker readiness", "GET", "{{gatewayUrl}}/e2e/worker-readyz", tests="""
if(pm.response.code===200){pm.collectionVariables.unset("brokerReadyPolls");pm.test("broker is ready",function(){pm.response.to.have.status(200);});}
else{const count=Number(pm.collectionVariables.get("brokerReadyPolls")||0)+1;pm.collectionVariables.set("brokerReadyPolls",count);if(count<80){pm.execution.setNextRequest(pm.info.requestName);}else{pm.test("broker recovered",function(){pm.expect.fail("broker readiness did not recover");});}}
"""),
        request("wait for broker ack and published state", "GET", "{{controlUrl}}/admin/state/outbox/latest?correlationId={{outboxEventIntentId}}", headers=admin_headers(), tests="""
const body=(pm.response.json().data || pm.response.json());
if(pm.response.code===200 && body.eventId===pm.collectionVariables.get("outboxEventId") && body.status==="published"){
  pm.collectionVariables.set("outboxAttempts",body.attempts);
  pm.test("published only after recovery",function(){pm.expect(body.attempts).to.be.at.least(1);});
}else{
  const count=Number(pm.collectionVariables.get("outboxPolls")||0)+1; pm.collectionVariables.set("outboxPolls",count);
  if(count<60){pm.execution.setNextRequest(pm.info.requestName);}else{pm.test("outbox recovered",function(){pm.expect.fail("outbox did not recover");});}
}
"""),
        request("test consumer sees event once", "GET", "{{controlUrl}}/admin/state/consumer?eventId={{outboxEventId}}", headers=admin_headers(), tests="""
if(pm.response.code===200 && (pm.response.json().data || pm.response.json()).deliveries===1){pm.test("one consumer delivery",function(){pm.expect((pm.response.json().data || pm.response.json()).deliveries).to.eql(1);});}
else{const count=Number(pm.collectionVariables.get("consumerPolls")||0)+1; pm.collectionVariables.set("consumerPolls",count); if(count<80){pm.execution.setNextRequest(pm.info.requestName);}else{pm.test("consumer recovered without duplicates",function(){pm.expect.fail("consumer delivery count was not one");});}}
"""),
    ]
    return {"name": "outbox-broker-recovery", "item": items}


def operator_folder():
    items = email_signin("operatorSignin")
    items += [
        request("create signed User status proof", "POST", "{{providerUrl}}/admin/proofs/user-status", headers=admin_headers(), body={"userId": "{{userId}}", "accountStatus": "restricted", "userVersion": 2}, tests=status_test(200, 'pm.collectionVariables.set("userStatusProof",(pm.response.json().data || pm.response.json()).proof);')),
        request("User status requires authorization decision", "PUT", "{{gatewayUrl}}/api/v1/operator/auth/users/{{userId}}/account-status", headers=bearer(), body={"userStatusChangeProof": "{{userStatusProof}}"}, tests=status_test(403)),
        request("operator policy read requires authorization", "GET", "{{gatewayUrl}}/api/v1/operator/auth/policies", headers=bearer(), tests=status_test(403)),
        request("manual action validates approval input", "POST", "{{gatewayUrl}}/api/v1/operator/auth/manual-actions", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}"), ("X-Authorization-Decision", "untrusted")], body={"caseId": "", "target": {"type": "session", "id": "{{sessionId}}"}, "action": "revoke_sessions", "reasonCode": "E2E_TEST", "approvalId": "", "evidenceRef": "", "expectedTargetVersion": 0}, tests=status_test(400)),
        request("untrusted operator decision is denied", "POST", "{{gatewayUrl}}/api/v1/operator/auth/manual-actions", headers=bearer() + [("Idempotency-Key", "{{$randomUUID}}"), ("X-Authorization-Decision", "untrusted")], body={"caseId": "case-e2e", "target": {"type": "session", "id": "{{sessionId}}"}, "action": "revoke_sessions", "reasonCode": "E2E_TEST", "approvalId": "approval-e2e", "evidenceRef": "evidence-e2e", "expectedTargetVersion": 0}, tests=status_test(403)),
    ]
    return {"name": "user-proof-operator-inputs", "item": items}


def dependency_restart_folder():
    items = email_signin("restartSignin")
    items += [
        request("prime cache before restarts", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
        request("restart Redis", "POST", "{{controlUrl}}/admin/containers/redis/restart", headers=admin_headers(), body={}, tests=status_test(200)),
        request("Redis restart falls back without data loss", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
        request("restart PostgreSQL", "POST", "{{controlUrl}}/admin/containers/postgres/restart", headers=admin_headers(), body={}, tests=status_test(200)),
        request("wait for auth after PostgreSQL restart", "GET", "{{gatewayUrl}}/e2e/auth-readyz", tests="""
if(pm.response.code===200){pm.test("auth ready after PostgreSQL restart",function(){pm.response.to.have.status(200);});}
else{const count=Number(pm.collectionVariables.get("postgresRestartPolls")||0)+1;pm.collectionVariables.set("postgresRestartPolls",count);if(count<20){pm.execution.setNextRequest(pm.info.requestName);}else{pm.test("auth recovered",function(){pm.expect.fail("auth did not recover");});}}
"""),
        request("Session survives PostgreSQL restart", "GET", "{{gatewayUrl}}/protected", headers=bearer(), tests=status_test(200)),
        request("restart Provider", "POST", "{{controlUrl}}/admin/containers/auth-provider/restart", headers=admin_headers(), body={}, tests=status_test(200)),
        request("restart auth worker", "POST", "{{controlUrl}}/admin/containers/auth-worker/restart", headers=admin_headers(), body={}, tests=status_test(200)),
    ]
    items += phone_signin("restartPhone", save_main=False)
    items += [
        request("restarted dependencies keep durable Session", "GET", "{{controlUrl}}/admin/state/session?sessionId={{restartSigninSessionId}}", headers=admin_headers(), tests=status_test(200, 'pm.test("Session remains active",function(){pm.expect((pm.response.json().data || pm.response.json()).status).to.eql("active");});')),
        request("Provider did not duplicate accepted deliveries", "GET", "{{providerUrl}}/admin/provider/stats", headers=admin_headers(), tests=status_test(200, 'pm.test("accepted deliveries stay unique",function(){pm.expect((pm.response.json().data || pm.response.json()).acceptedUnique).to.be.at.least(1);});')),
    ]
    return {"name": "dependency-restart-recovery", "item": items}


def build_collection():
    init_script = """
if (!pm.collectionVariables.get("runId")) {
  const runId = pm.variables.replaceIn("{{$randomUUID}}");
  const digits = String(Date.now()).slice(-8);
  pm.collectionVariables.set("runId", runId);
  pm.collectionVariables.set("email", "auth-" + runId + "@example.test");
  pm.collectionVariables.set("phoneNational", "10" + digits);
  pm.collectionVariables.set("phoneEncoded", "+82" + "10" + digits);
  pm.collectionVariables.set("linkPhoneNational", "11" + digits);
  pm.collectionVariables.set("linkPhoneEncoded", "+82" + "11" + digits);
  pm.collectionVariables.set("replacementPhoneNational", "12" + digits);
  pm.collectionVariables.set("replacementPhoneEncoded", "+82" + "12" + digits);
  pm.collectionVariables.set("userId", pm.variables.replaceIn("{{$randomUUID}}"));
  pm.collectionVariables.set("initialPassword", "E2e-Strong-Password!42");
  pm.collectionVariables.set("newPassword", "E2e-New-Strong-Password!43");
  pm.collectionVariables.set("currentPassword", pm.collectionVariables.get("initialPassword"));
}
"""
    return {
        "info": {
            "_postman_id": "22cc03fd-6fde-4b43-b4d8-9d6b9b53e201",
            "name": "Auth service dependency E2E",
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "event": [{"listen": "prerequest", "script": {"type": "text/javascript", "exec": script_lines(init_script)}}],
        "variable": [
            {"key": "gatewayUrl", "value": "http://auth-gateway:8080"},
            {"key": "providerUrl", "value": "http://auth-provider:8080"},
            {"key": "controlUrl", "value": "http://auth-control:8080"},
            {"key": "edgeProbeUrl", "value": "http://auth-edge-probe:8080"},
            {"key": "adminToken", "value": ""},
        ],
        "item": [
            startup_folder(), registration_folder(), signin_folder(), gateway_folder(), session_folder(),
            password_reset_folder(), identity_folder(), session_timeout_folder(), redis_fallback_folder(),
            provider_folder(), outbox_folder(), operator_folder(), dependency_restart_folder(),
        ],
    }


def main():
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT.write_text(json.dumps(build_collection(), ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
