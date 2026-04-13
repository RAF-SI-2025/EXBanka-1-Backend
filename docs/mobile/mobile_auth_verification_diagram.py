#!/usr/bin/env python3
"""Generate mobile auth + verification sequence diagram as PNG."""

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.patches as mpatches

# ---------------------------------------------------------------------------
# Canvas
# ---------------------------------------------------------------------------
W, H = 32, 95
fig, ax = plt.subplots(figsize=(W, H))
ax.set_xlim(0, W)
ax.set_ylim(0, H)
ax.axis("off")
fig.patch.set_facecolor("#FFFFFF")

# ---------------------------------------------------------------------------
# Lifeline X positions — wide spacing
# ---------------------------------------------------------------------------
M  = 4      # Mobile App
G  = 11     # API Gateway
A  = 18     # Auth / Verification Service
N  = 25     # Notification / Transaction Service
N2 = 29     # Notification (phase 2 only, narrow)

# ---------------------------------------------------------------------------
# Colours
# ---------------------------------------------------------------------------
C_HEAD     = "#2C3E50"
C_LINE     = "#95A5A6"
C_ARROW    = "#2471A3"
C_KAFKA    = "#C0392B"
C_NOTE     = "#FEF9E7"
C_OK       = "#1E8449"
C_ACTOR_BG = "#D6EAF8"
C_PHASE_BG = "#F8F9F9"
C_DIVIDER  = "#E74C3C"
C_WARN     = "#FADBD8"
C_GREEN_N  = "#D5F5E3"
C_SIG      = "#FDEBD0"
C_SIG_BD   = "#E67E22"

FONT = "monospace"

ARROW_KW = dict(arrowstyle="->,head_width=0.35,head_length=0.25",
                color=C_ARROW, lw=2)
KAFKA_KW = dict(arrowstyle="->,head_width=0.35,head_length=0.25",
                color=C_KAFKA, lw=2, linestyle="dashed")
RET_KW   = dict(arrowstyle="->,head_width=0.3,head_length=0.2",
                color=C_OK, lw=1.6, linestyle="dotted")

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
def actor_box(x, y, label):
    bw, bh = 3.6, 0.9
    ax.add_patch(mpatches.FancyBboxPatch(
        (x - bw/2, y - bh/2), bw, bh,
        boxstyle="round,pad=0.15", facecolor=C_ACTOR_BG,
        edgecolor=C_HEAD, lw=2.5))
    ax.text(x, y, label, ha="center", va="center",
            fontsize=11, fontweight="bold", color=C_HEAD, family=FONT)

def lifeline(x, y_top, y_bot):
    ax.plot([x, x], [y_top, y_bot], color=C_LINE, lw=1, ls="--", zorder=0)

def msg(x1, x2, y, label, kafka=False):
    kw = dict(KAFKA_KW) if kafka else dict(ARROW_KW)
    ax.annotate("", xy=(x2, y), xytext=(x1, y), arrowprops=kw)
    mid = (x1 + x2) / 2
    color = C_KAFKA if kafka else C_ARROW
    ax.text(mid, y + 0.25, label, ha="center", va="bottom",
            fontsize=9.5, color=color, family=FONT, fontweight="bold",
            bbox=dict(boxstyle="round,pad=0.1", facecolor="white",
                      edgecolor="none", alpha=0.95), zorder=4)

def ret(x1, x2, y, label):
    ax.annotate("", xy=(x2, y), xytext=(x1, y), arrowprops=RET_KW)
    mid = (x1 + x2) / 2
    ax.text(mid, y + 0.22, label, ha="center", va="bottom",
            fontsize=9, color=C_OK, family=FONT, style="italic",
            bbox=dict(boxstyle="round,pad=0.08", facecolor="white",
                      edgecolor="none", alpha=0.95), zorder=4)

def note(x, y, text, w=3.6, h=0.8, color=C_NOTE):
    ax.add_patch(mpatches.FancyBboxPatch(
        (x - w/2, y - h/2), w, h,
        boxstyle="round,pad=0.12", facecolor=color,
        edgecolor="#B7950B", lw=1, alpha=0.92))
    ax.text(x, y, text, ha="center", va="center",
            fontsize=8.5, color="#5D4E0B", family=FONT)

def self_note(x, y, label):
    """Internal action — no arrow, just a boxed italic note to the right."""
    ax.add_patch(mpatches.FancyBboxPatch(
        (x + 0.6, y - 0.4), 10.5, 0.8,
        boxstyle="round,pad=0.12", facecolor="#FAFAFA",
        edgecolor="#D5D8DC", lw=1, alpha=0.9, zorder=2))
    ax.plot([x, x + 0.6], [y, y], color=C_HEAD, lw=1.2, zorder=2)
    ax.text(x + 0.9, y, label, ha="left", va="center",
            fontsize=9, color=C_HEAD, family=FONT, style="italic", zorder=3)

def phase_title(y, label):
    ax.plot([0.8, W - 0.8], [y, y], color=C_DIVIDER, lw=3, zorder=5)
    bw = 16
    ax.add_patch(mpatches.FancyBboxPatch(
        (W/2 - bw/2, y - 0.4), bw, 0.8,
        boxstyle="round,pad=0.18", facecolor=C_DIVIDER,
        edgecolor=C_DIVIDER, lw=0, zorder=6))
    ax.text(W/2, y, label, ha="center", va="center",
            fontsize=14, fontweight="bold", color="white", family=FONT, zorder=7)

def section_bg(y_top, y_bot, label):
    ax.add_patch(mpatches.FancyBboxPatch(
        (1.0, y_bot), W - 2, y_top - y_bot,
        boxstyle="round,pad=0.1", facecolor=C_PHASE_BG,
        edgecolor="#D5D8DC", lw=0.8, alpha=0.35, zorder=-1))
    ax.text(1.4, (y_top + y_bot) / 2, label, ha="left", va="center",
            fontsize=9, color="#7F8C8D", family=FONT, rotation=90,
            fontweight="bold")

# Vertical step spacing
S = 1.6   # standard gap between steps
SH = 1.2  # smaller gap (return arrows, etc.)

# ===========================================================================
#  TITLE
# ===========================================================================
ax.text(W/2, H - 0.9, "EXBanka-1 Team",
        ha="center", va="center", fontsize=22, fontweight="bold",
        color=C_HEAD, family=FONT)
ax.text(W/2, H - 1.6, "Mobile Authentication & Verification Timeline",
        ha="center", va="center", fontsize=16, color=C_HEAD, family=FONT)

# ===========================================================================
#  PART 1 — MOBILE AUTHENTICATION
# ===========================================================================
top1 = H - 3.0
for lbl, x in [("Mobile App", M), ("API Gateway", G),
               ("Auth Service", A), ("Notification\nService", N)]:
    actor_box(x, top1, lbl)

phase_title(top1 - 1.3, "PHASE 1 — DEVICE ACTIVATION")

p1_ll_top = top1 - 0.5

# ── 1A: Request Activation Code ───────────────────────────────────────────
sec_a_top = top1 - 2.0
y = sec_a_top - 0.6

msg(M, G, y, "POST /api/mobile/auth/request-activation")
note(M, y - 0.8, '{ "email": "user@bank.com" }', w=3.6, h=0.7)

y -= S
msg(G, A, y, "gRPC: RequestMobileActivation(email)")

y -= S
self_note(A, y, "Generate 6-digit code, store in DB (15min expiry, max 3 attempts)")

y -= S
msg(A, N, y, "Kafka: notification.send-email", kafka=True)
note(N, y - 0.8, "Email with\nactivation code", w=3.2, h=0.7)

y -= SH
ret(A, G, y, "200 OK")
y -= SH
ret(G, M, y, '{ "message": "activation code sent" }')
sec_a_bot = y - 0.4
section_bg(sec_a_top, sec_a_bot, "Request\nCode")

# ── 1B: Activate Device ──────────────────────────────────────────────────
y -= S * 0.6
sec_b_top = y
y -= 0.6

msg(M, G, y, "POST /api/mobile/auth/activate")
note(M, y - 0.8, "{ email, code, device_name }", w=3.6, h=0.7)

y -= S
msg(G, A, y, "gRPC: ActivateMobileDevice(email, code, device_name)")

y -= S
self_note(A, y, "SELECT FOR UPDATE activation code")
y -= SH
self_note(A, y, "Validate: exists, not used, not expired, attempts < 3")

y -= S
self_note(A, y, "Deactivate all existing devices (one device per user)")
y -= SH
self_note(A, y, "Create MobileDevice (device_id=UUID, device_secret=64-hex)")

y -= S
self_note(A, y, "Generate access token (15min, device_type=mobile) + refresh token (90 days)")

y -= SH
msg(A, N, y, "Kafka: auth.mobile-device-activated", kafka=True)

y -= SH
ret(A, G, y, "tokens + device credentials")
y -= SH
ret(G, M, y, "{ access_token, refresh_token, device_id, device_secret }")

y -= SH
note(M, y - 0.5, "Store device_secret in\nKeychain (iOS) / Keystore (Android)\nReturned ONLY ONCE!",
     w=4.0, h=1.0, color=C_WARN)
sec_b_bot = y - 1.2
section_bg(sec_b_top, sec_b_bot, "Activate\nDevice")

# ── 1C: Token Refresh ────────────────────────────────────────────────────
y -= S * 1.5
sec_c_top = y
y -= 0.6

msg(M, G, y, "POST /api/mobile/auth/refresh")
note(M, y - 0.8, "{ refresh_token }\nHeader: X-Device-ID", w=3.6, h=0.7)

y -= S
msg(G, A, y, "gRPC: RefreshMobileToken(refresh_token, device_id)")

y -= S
self_note(A, y, "Validate refresh token (not revoked, not expired)")
y -= SH
self_note(A, y, "Verify device_id matches active device")
y -= SH
self_note(A, y, "Revoke old refresh token, issue new pair")

y -= SH
ret(A, G, y, "new token pair")
y -= SH
ret(G, M, y, "{ access_token, refresh_token }")
sec_c_bot = y - 0.4
section_bg(sec_c_top, sec_c_bot, "Token\nRefresh")

# ── 1D: Authenticated Requests ───────────────────────────────────────────
y -= S * 0.6
sec_d_top = y
y -= 0.6

msg(M, G, y, "ANY  /api/me/*  or  /api/v1/mobile/*")
note(M, y - 0.8, "Bearer: access_token\nX-Device-ID: <uuid>", w=3.6, h=0.7)

y -= S
self_note(G, y, "Extract Bearer token from Authorization header")

y -= S
msg(G, A, y, "gRPC: ValidateToken(token)")

y -= S
self_note(A, y, "Verify JWT signature (HS256 + JWT_SECRET)")
y -= SH
self_note(A, y, "Check token not expired, extract claims (user_id, roles, permissions, device_id)")

y -= SH
ret(A, G, y, "{ valid, user_id, email, roles, permissions, system_type, device_id }")

y -= S
self_note(G, y, "MobileAuthMiddleware: check device_type=mobile, match X-Device-ID with JWT claim")
y -= SH
self_note(G, y, "AuthMiddleware: block client tokens from employee routes (system_type check)")
y -= SH
self_note(G, y, "RequirePermission: check user has required permission for this endpoint")

y -= S
self_note(G, y, "Set context: user_id, email, roles, permissions, device_id")

y -= SH
msg(G, A, y, "Forward to appropriate backend service")
y -= SH
ret(A, G, y, "response")
y -= SH
ret(G, M, y, "response")
sec_d_bot = y - 0.4
section_bg(sec_d_top, sec_d_bot, "Authed\nRequests")

p1_ll_bot = y - 0.5

# Draw lifelines for Part 1
for x in [M, G, A, N]:
    lifeline(x, p1_ll_top, p1_ll_bot)


# ===========================================================================
#  BRIDGE — SECURITY LAYERS (between phases)
# ===========================================================================
y -= S * 1.2

# ── Box 1: JWT Signature Verification ──
jwt_top = y
jwt_h = 4.0
jwt_w = W - 4
jwt_x = W / 2

ax.add_patch(mpatches.FancyBboxPatch(
    (jwt_x - jwt_w/2, jwt_top - jwt_h), jwt_w, jwt_h,
    boxstyle="round,pad=0.3", facecolor="#D4E6F1",
    edgecolor="#2471A3", lw=3, alpha=0.95, zorder=2))

cy = jwt_top - 0.6
ax.text(jwt_x, cy, "SECURITY LAYER 1 — JWT SIGNATURE VERIFICATION (every request)",
        ha="center", va="center", fontsize=14, fontweight="bold",
        color="#1A5276", family=FONT, zorder=3)

cy -= 1.0
ax.text(jwt_x, cy,
        "The API Gateway validates EVERY request by calling auth-service via gRPC: ValidateToken(token).\n"
        "Auth-service verifies the HS256 signature using JWT_SECRET and checks token expiry.",
        ha="center", va="center", fontsize=11, color=C_HEAD, family=FONT, zorder=3)

cy -= 1.1
ax.text(jwt_x, cy,
        "JWT claims extracted:   user_id  |  email  |  roles []  |  permissions []  |  "
        "system_type  |  device_type  |  device_id",
        ha="center", va="center", fontsize=10.5, fontweight="bold",
        color="#1A5276", family=FONT, zorder=3,
        bbox=dict(boxstyle="round,pad=0.15", facecolor="white",
                  edgecolor="#AED6F1", lw=1.5, alpha=0.9))

cy -= 1.0
ax.text(jwt_x, cy,
        "Gateway enforces:   AuthMiddleware (employee routes)  |  "
        "AnyAuthMiddleware (/api/me/*)  |  MobileAuthMiddleware (device_type + device_id match)\n"
        "RequirePermission checks role-based access.  "
        "Client tokens are BLOCKED from employee-only routes (system_type check).",
        ha="center", va="center", fontsize=10, color=C_HEAD, family=FONT, zorder=3)

y = jwt_top - jwt_h

# ── Box 2: Device Signature ──
y -= S * 0.6
sig_top = y
sig_h = 4.0
sig_w = W - 4
sig_x = W / 2

ax.add_patch(mpatches.FancyBboxPatch(
    (sig_x - sig_w/2, sig_top - sig_h), sig_w, sig_h,
    boxstyle="round,pad=0.3", facecolor=C_SIG,
    edgecolor=C_SIG_BD, lw=3, alpha=0.95, zorder=2))

cy = sig_top - 0.6
ax.text(sig_x, cy, "SECURITY LAYER 2 — DEVICE SIGNATURE (secure routes only)",
        ha="center", va="center", fontsize=14, fontweight="bold",
        color="#D35400", family=FONT, zorder=3)

cy -= 1.0
ax.text(sig_x, cy,
        "RequireDeviceSignature middleware adds a SECOND check on top of JWT for sensitive endpoints\n"
        "(verification, transactions, device management).",
        ha="center", va="center", fontsize=11, color=C_HEAD, family=FONT, zorder=3)

cy -= 1.1
ax.text(sig_x, cy,
        "Signature  =  HMAC-SHA256( device_secret,  timestamp : method : path : SHA256(body) )",
        ha="center", va="center", fontsize=12, fontweight="bold",
        color="#6C3483", family=FONT, zorder=3,
        bbox=dict(boxstyle="round,pad=0.15", facecolor="white",
                  edgecolor="#D7BDE2", lw=1.5, alpha=0.9))

cy -= 0.9
ax.text(sig_x, cy,
        "Required headers:   X-Device-Timestamp (unix epoch, +/- 30s window)    "
        "X-Device-Signature (hex digest)\n"
        "device_secret is NEVER sent over the wire — it stays in Keychain/Keystore "
        "and is only used locally to compute the HMAC.",
        ha="center", va="center", fontsize=10, color=C_HEAD, family=FONT, zorder=3)

y = sig_top - sig_h


# ===========================================================================
#  PART 2 — TRANSACTION VERIFICATION
# ===========================================================================
y -= S * 0.8
phase_title(y, "PHASE 2 — TRANSACTION VERIFICATION")

y -= 1.0
top2 = y
for lbl, x in [("Mobile App", M), ("API Gateway", G),
               ("Verification\nService", A), ("Transaction\nService", N)]:
    actor_box(x, top2, lbl)

V = A
T = N
p2_ll_top = top2 - 0.5

# ── 2A: Create Payment ───────────────────────────────────────────────────
y -= 1.5
sec_2a_top = y
y -= 0.6

msg(M, G, y, "POST /api/me/payments")
note(M, y - 0.8, "{ from_account, to_account,\n  amount, currency }", w=3.8, h=0.8)

y -= S
msg(G, T, y, "gRPC: CreatePayment(...)")

y -= S
self_note(T, y, "Save payment with status = pending_verification (NO balance changes yet)")

y -= SH
ret(T, G, y, "payment (pending_verification)")
y -= SH
ret(G, M, y, '{ payment_id, status: "pending_verification" }')
section_bg(sec_2a_top, y - 0.4, "Create\nPayment")

# ── 2B: Create Verification Challenge ────────────────────────────────────
y -= S * 0.8
sec_2b_top = y
y -= 0.6

msg(M, G, y, "POST /api/verifications")
note(M, y - 0.85, '{ source_service: "payment",\n  source_id: payment_id,\n  method: "code_pull" }',
     w=3.8, h=1.0)

y -= S * 1.1
msg(G, V, y, "gRPC: CreateChallenge(user_id, payment, id, code_pull)")

y -= S
self_note(V, y, "Generate 6-digit verification code, create challenge (pending, 5min expiry)")

y -= S
msg(V, T, y, "Kafka: verification.challenge-created", kafka=True)
note(T + 0.3, y - 0.8, "Notification service creates\nMobileInboxItem + pushes via\nKafka -> WebSocket to mobile", w=4.2, h=0.9)

y -= SH
ret(V, G, y, "{ challenge_id, expires_at }")
y -= SH
ret(G, M, y, "{ challenge_id, expires_at }")
section_bg(sec_2b_top, y - 0.4, "Create\nChallenge")

# ── 2C: Mobile Receives & Submits ────────────────────────────────────────
y -= S * 0.8
sec_2c_top = y
y -= 0.6

msg(M, G, y, "GET /api/v1/mobile/verifications/pending")
note(M, y - 0.8, "SIGNED REQUEST\n(DeviceSignature required)", w=3.8, h=0.7, color=C_SIG)

y -= S
ret(G, M, y, "{ challenge_id, method, display_data: { code }, expires_at }")

y -= S
note(M, y, "User receives push notification\nwith verification challenge", w=3.8, h=0.7, color=C_GREEN_N)

# ── Helper for OR divider ──
def or_divider(y_pos):
    ax.plot([2.0, W - 2.0], [y_pos, y_pos], color="#7F8C8D", lw=1.5, ls="-.", zorder=3)
    ax.add_patch(mpatches.FancyBboxPatch(
        (W/2 - 1.5, y_pos - 0.25), 3.0, 0.5,
        boxstyle="round,pad=0.12", facecolor="white",
        edgecolor="#7F8C8D", lw=1.5, zorder=4))
    ax.text(W/2, y_pos, "— OR —", ha="center", va="center",
            fontsize=11, fontweight="bold", color="#7F8C8D", family=FONT, zorder=5)

def option_label(y_pos, label, desc, bg_color, edge_color, text_color):
    ax.add_patch(mpatches.FancyBboxPatch(
        (1.8, y_pos - 0.25), 2.4, 0.5,
        boxstyle="round,pad=0.1", facecolor=bg_color,
        edgecolor=edge_color, lw=1.5))
    ax.text(3.0, y_pos, label, ha="center", va="center",
            fontsize=9, fontweight="bold", color=text_color, family=FONT)
    ax.text(5.6, y_pos, desc, ha="left", va="center",
            fontsize=9, color=C_HEAD, family=FONT, style="italic")

# ======================================================================
# Option A: Browser — user types the 6-digit code in the web app
# ======================================================================
y -= S * 0.8
option_label(y, "OPTION A",
             "Browser — user types the 6-digit code in the web app",
             "#E8DAEF", "#8E44AD", "#6C3483")

y -= S * 0.8
msg(M, G, y, "POST /api/verifications/{id}/code")
note(M, y - 0.8, '{ code: "123456" }\nNo device signature needed\n(browser has no device_secret)', w=4.2, h=0.9)

y -= S * 1.1
msg(G, V, y, "gRPC: SubmitCode(challenge_id, code)")

y -= S
self_note(V, y, "SELECT FOR UPDATE challenge, validate pending/not expired/attempts < 3")
y -= SH
self_note(V, y, "Compare code -> mark status = verified")

y -= SH
msg(V, T, y, "Kafka: verification.challenge-verified", kafka=True)

# ── OR ──
y -= S * 0.7
or_divider(y)

# ======================================================================
# Option B: Mobile PIN — user enters PIN in app, app submits signed request
# ======================================================================
y -= S * 0.8
option_label(y, "OPTION B",
             "Mobile PIN — user enters PIN in-app to authorize, app submits code",
             "#AED6F1", "#2980B9", "#1A5276")

y -= S * 0.8
note(M, y, "User enters app PIN to unlock\nthe verify action", w=3.8, h=0.7, color="#AED6F1")

y -= S
msg(M, G, y, "POST /api/v1/mobile/verifications/{id}/submit")
note(M, y - 0.8, '{ response: "123456" }\nSIGNED REQUEST', w=3.8, h=0.7, color=C_SIG)

y -= S
msg(G, V, y, "gRPC: SubmitVerification(challenge_id, device_id, code)")

y -= S
self_note(V, y, "SELECT FOR UPDATE challenge, validate pending/not expired/attempts < 3")
y -= SH
self_note(V, y, "Compare code -> mark status = verified")

y -= SH
msg(V, T, y, "Kafka: verification.challenge-verified", kafka=True)

# ── OR ──
y -= S * 0.7
or_divider(y)

# ======================================================================
# Option C: Mobile Biometric — fingerprint/face ID instead of PIN (faster)
# ======================================================================
y -= S * 0.8
option_label(y, "OPTION C",
             "Mobile Biometric — fingerprint / Face ID instead of PIN (faster, no code entry)",
             "#D5F5E3", "#27AE60", "#1E8449")

y -= S * 0.8
note(M, y, "User authenticates with\nfingerprint or Face ID", w=3.8, h=0.7, color="#D5F5E3")

y -= S
msg(M, G, y, "POST /api/v1/mobile/verifications/{id}/biometric")
note(M, y - 0.8, "SIGNED REQUEST\nNo code needed — biometrics\nreplaces both PIN and code", w=4.0, h=0.9, color=C_SIG)

y -= S * 1.1
msg(G, V, y, "gRPC: VerifyByBiometric(challenge_id, user_id, device_id)")

y -= S
self_note(V, y, "SELECT FOR UPDATE challenge, validate pending/not expired")
y -= SH
self_note(V, y, "Check biometrics_enabled via auth-service (gRPC: CheckBiometricsEnabled)")
y -= SH
self_note(V, y, "Mark status = verified, set verified_by = biometric (audit trail)")

y -= SH
msg(V, T, y, "Kafka: verification.challenge-verified", kafka=True)

# ── End of options ──
y -= S * 0.5
ax.plot([2.0, W - 2.0], [y, y], color="#27AE60", lw=2, zorder=3)
ax.add_patch(mpatches.FancyBboxPatch(
    (W/2 - 4, y - 0.25), 8, 0.5,
    boxstyle="round,pad=0.12", facecolor="#D5F5E3",
    edgecolor="#27AE60", lw=1.5, zorder=4))
ax.text(W/2, y, "All options lead to Execute Payment below",
        ha="center", va="center", fontsize=9, fontweight="bold",
        color="#1E8449", family=FONT, zorder=5)

section_bg(sec_2c_top, y - 0.5, "Verify\nChallenge")

# ── 2D: Execute Transaction ──────────────────────────────────────────────
y -= S * 0.8
sec_2d_top = y
y -= 0.6

msg(M, G, y, "POST /api/payments/{id}/execute")
note(M, y - 0.8, '{ challenge_id: "..." }', w=3.6, h=0.6)

y -= S
msg(G, T, y, "gRPC: ExecutePayment(payment_id, challenge_id)")

y -= S
msg(T, V, y, "gRPC: GetChallengeStatus(challenge_id)")
y -= SH
ret(V, T, y, 'status: "verified"')

y -= S
self_note(T, y, "Validate payment status == pending_verification")
y -= SH
self_note(T, y, "Re-check spending limits (daily / monthly)")

y -= S
self_note(T, y, "SAGA: Debit sender -> Credit recipient -> Credit bank commission")
y -= SH
self_note(T, y, "(automatic compensation on any step failure)")

y -= S
self_note(T, y, "Mark payment status = completed")

y -= SH
msg(T, V, y, "Kafka: transaction.payment-completed", kafka=True)

y -= SH
ret(T, G, y, "payment (completed)")
y -= SH
ret(G, M, y, '{ status: "completed" }')
section_bg(sec_2d_top, y - 0.4, "Execute\nPayment")

p2_ll_bot = y - 0.5

# Draw lifelines for Part 2
for x in [M, G, V, T]:
    lifeline(x, p2_ll_top, p2_ll_bot)


# ===========================================================================
#  LEGEND
# ===========================================================================
leg_y = y - 1.5
ax.plot([1.5, W - 1.5], [leg_y + 0.5, leg_y + 0.5], color="#BDC3C7", lw=1.5)

ax.text(2, leg_y, "LEGEND:", fontsize=11, fontweight="bold",
        color=C_HEAD, family=FONT, va="center")

ax.annotate("", xy=(6.5, leg_y), xytext=(4.5, leg_y), arrowprops=ARROW_KW)
ax.text(7, leg_y, "Sync (gRPC / HTTP)", fontsize=10, va="center",
        color=C_ARROW, family=FONT)

ax.annotate("", xy=(15.5, leg_y), xytext=(13.5, leg_y), arrowprops=KAFKA_KW)
ax.text(16, leg_y, "Async (Kafka)", fontsize=10, va="center",
        color=C_KAFKA, family=FONT)

ax.annotate("", xy=(23.5, leg_y), xytext=(21.5, leg_y), arrowprops=RET_KW)
ax.text(24, leg_y, "Response", fontsize=10, va="center",
        color=C_OK, family=FONT)

leg_y -= 0.9
ax.text(2, leg_y,
        "Token Lifetimes:   Access = 15 min   |   Refresh (mobile) = 90 days   |   "
        "Activation Code = 15 min   |   Verification Challenge = 5 min",
        fontsize=10, color=C_HEAD, family=FONT, va="center")

# ---------------------------------------------------------------------------
# Resize canvas to fit all content
# ---------------------------------------------------------------------------
bottom = leg_y - 1.5
ax.set_ylim(bottom, H)
fig.set_size_inches(W, (H - bottom) * 0.85)

# ---------------------------------------------------------------------------
# Save
# ---------------------------------------------------------------------------
out = "/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend/docs/mobile_auth_verification_timeline.png"
fig.savefig(out, dpi=160, bbox_inches="tight", pad_inches=0.6)
plt.close()
print(f"Saved -> {out}")
