#!/usr/bin/env python3
"""
Generate database ER diagrams for each service from GORM model Go files.
Uses PIL/Pillow only (no external dependencies needed).
"""

import os
import re
import math
import glob
from PIL import Image, ImageDraw, ImageFont

BASE_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUTPUT_DIR = os.path.dirname(os.path.abspath(__file__))

SERVICES = [
    ("auth-service",         "auth_db"),
    ("user-service",         "user_db"),
    ("client-service",       "client_db"),
    ("account-service",      "account_db"),
    ("card-service",         "card_db"),
    ("transaction-service",  "transaction_db"),
    ("credit-service",       "credit_db"),
    ("exchange-service",     "exchange_db"),
    ("verification-service", "verification_db"),
    ("notification-service", "notification_db"),
    ("stock-service",        "stock_db"),
]

BG_COLOR            = (248, 249, 252)
TABLE_HEADER_BG     = (45, 85, 160)
TABLE_HEADER_FG     = (255, 255, 255)
TABLE_ROW_ALT       = (235, 240, 250)
TABLE_ROW_EVEN      = (252, 253, 255)
TABLE_BORDER        = (160, 175, 200)
TEXT_COLOR          = (30, 30, 40)
PK_COLOR            = (20, 130, 50)
FK_COLOR            = (180, 60, 60)
UK_COLOR            = (30, 100, 190)
RELATION_COLOR      = (180, 60, 60)
JOIN_TABLE_HEADER   = (200, 130, 30)
JOIN_TABLE_ROW      = (255, 245, 220)
GRID_COLOR          = (230, 233, 240)
TITLE_COLOR         = (30, 50, 100)


def parse_go_models(model_dir):
    """Parse Go struct definitions from model files."""
    tables = []
    join_tables = []

    go_files = glob.glob(os.path.join(model_dir, "*.go"))
    for filepath in sorted(go_files):
        with open(filepath, "r") as f:
            content = f.read()

        struct_pattern = re.compile(
            r'type\s+(\w+)\s+struct\s*\{([^}]*(?:\{[^}]*\}[^}]*)*)\}',
            re.DOTALL
        )
        for match in struct_pattern.finditer(content):
            struct_name = match.group(1)
            body = match.group(2)

            if "gorm:" not in body and "ID" not in body:
                continue

            fields = []
            for line in body.strip().split("\n"):
                line = line.strip()
                if not line or line.startswith("//") or line.startswith("*"):
                    continue

                field_match = re.match(
                    r'(\w+)\s+([\w.*\[\]]+(?:\.\w+)?)\s*(?:`([^`]*)`)?',
                    line
                )
                if not field_match:
                    continue

                field_name  = field_match.group(1)
                field_type  = field_match.group(2)
                tags        = field_match.group(3) or ""

                if field_name in ("gorm", "Model"):
                    continue

                m2m_match = re.search(r'many2many:(\w+)', tags)
                if m2m_match:
                    join_tables.append((struct_name, field_name, m2m_match.group(1)))

                # Skip slice / pointer-to-slice (relationship fields, not columns)
                if field_type.startswith("[]") or (field_type.startswith("*") and "[]" in field_type):
                    continue
                if field_type in ("Roles", "Permissions", "AdditionalPermissions"):
                    continue

                is_pk     = "primaryKey" in tags or field_name == "ID"
                is_unique = "uniqueIndex" in tags or ("unique" in tags and "uniqueIndex" not in tags)

                is_fk = (
                    (field_name.endswith("ID") and field_name != "ID")
                    or re.search(r'foreignKey:', tags) is not None
                )

                display_type = field_type.replace("*", "").replace("decimal.", "").replace("datatypes.", "")
                if display_type.startswith("time."):
                    display_type = "timestamp"
                if display_type == "Decimal":
                    display_type = "decimal"
                if display_type == "JSON":
                    display_type = "jsonb"
                if display_type == "DeletedAt":
                    display_type = "timestamp"

                marker = ""
                if is_pk:
                    marker = "PK"
                elif is_unique:
                    marker = "UK"
                elif is_fk:
                    marker = "FK"

                fields.append((field_name, display_type, marker))

            if fields:
                tables.append((struct_name, fields))

    return tables, join_tables


def load_fonts():
    for font_path in [
        "/System/Library/Fonts/SFNSMono.ttf",
        "/System/Library/Fonts/Menlo.ttc",
        "/System/Library/Fonts/Monaco.ttf",
        "/Library/Fonts/Courier New.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
        "/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",
    ]:
        if os.path.exists(font_path):
            try:
                return (
                    ImageFont.truetype(font_path, 18),  # title
                    ImageFont.truetype(font_path, 13),  # header
                    ImageFont.truetype(font_path, 11),  # body
                )
            except Exception:
                continue
    d = ImageFont.load_default()
    return d, d, d


def text_size(draw, text, font):
    bbox = draw.textbbox((0, 0), text, font=font)
    return bbox[2] - bbox[0], bbox[3] - bbox[1]


ROW_H    = 22
PAD      = 8
MARKER_W = 32


def measure_table(draw, tname, fields, fh, fb):
    max_name_w = max((text_size(draw, n, fb)[0] for n, _, _ in fields), default=0)
    max_type_w = max((text_size(draw, t, fb)[0] for _, t, _ in fields), default=0)
    hdr_w = text_size(draw, tname, fh)[0]
    w = max(max_name_w + max_type_w + MARKER_W + PAD * 4, hdr_w + PAD * 2, 160)
    h = ROW_H + len(fields) * ROW_H
    return w, h


def draw_table(draw, x, y, tname, fields, fh, fb, is_join=False):
    """Draw a table. Returns (w, h, field_row_centers)."""
    w, h = measure_table(draw, tname, fields, fh, fb)

    max_name_w = max((text_size(draw, n, fb)[0] for n, _, _ in fields), default=0)

    # Header
    hdr_bg = JOIN_TABLE_HEADER if is_join else TABLE_HEADER_BG
    draw.rectangle([x, y, x + w, y + ROW_H], fill=hdr_bg, outline=TABLE_BORDER, width=1)
    draw.text((x + PAD, y + 4), tname, fill=TABLE_HEADER_FG, font=fh)

    field_centers = {}  # field_name → (left_x, right_x, center_y)

    for i, (name, ftype, marker) in enumerate(fields):
        ry  = y + ROW_H + i * ROW_H
        cy  = ry + ROW_H // 2
        row_bg = (JOIN_TABLE_ROW if is_join
                  else (TABLE_ROW_ALT if i % 2 == 0 else TABLE_ROW_EVEN))

        draw.rectangle([x, ry, x + w, ry + ROW_H], fill=row_bg, outline=TABLE_BORDER, width=1)

        # Marker badge
        if marker == "PK":
            draw.text((x + 4, ry + 4), "PK", fill=PK_COLOR, font=fb)
        elif marker == "FK":
            draw.text((x + 4, ry + 4), "FK", fill=FK_COLOR, font=fb)
        elif marker == "UK":
            draw.text((x + 4, ry + 4), "UK", fill=UK_COLOR, font=fb)

        # Field name
        name_color = FK_COLOR if marker == "FK" else TEXT_COLOR
        draw.text((x + MARKER_W, ry + 4), name, fill=name_color, font=fb)

        # Field type (right-aligned ish)
        draw.text((x + MARKER_W + max_name_w + PAD * 2, ry + 4), ftype, fill=(140, 140, 160), font=fb)

        field_centers[name] = (x, x + w, cy)

    return w, h, field_centers


def draw_arrowhead(draw, ex, ey, from_x, from_y, color, size=9):
    """Draw a filled arrowhead at (ex,ey) pointing away from (from_x, from_y)."""
    dx = ex - from_x
    dy = ey - from_y
    length = math.hypot(dx, dy)
    if length == 0:
        return
    ux, uy = dx / length, dy / length
    px, py = -uy, ux
    p1 = (int(ex - size * ux + size * 0.45 * px),
          int(ey - size * uy + size * 0.45 * py))
    p2 = (int(ex - size * ux - size * 0.45 * px),
          int(ey - size * uy - size * 0.45 * py))
    draw.polygon([p1, (int(ex), int(ey)), p2], fill=color)


def draw_fk_connector(draw, sx, sy, tx, ty, tw, th, color, label, fb):
    """
    Draw an orthogonal FK connector from source point (sx,sy) to the
    target table's left or right edge, ending with an arrowhead.
    Route: horizontal leg first, then vertical.
    """
    # Decide which side of the target table to connect to
    t_left  = tx
    t_right = tx + tw
    t_top   = ty
    t_bot   = ty + th

    # Clamp sy into target table vertical range
    target_cy = max(t_top + ROW_H // 2, min(t_bot - ROW_H // 2, sy))

    # Choose entry edge: left or right of target
    if sx <= t_left:
        ex, ey = t_left, target_cy
        corner_x = ex - 20
    elif sx >= t_right:
        ex, ey = t_right, target_cy
        corner_x = ex + 20
    else:
        # Source is horizontally within target — come in from the top
        ex = tx + tw // 2
        ey = t_top if sy < t_top else t_bot
        # Draw a simple right-angle from source down/up to entry
        mid_y = ey + (-25 if ey == t_top else 25)
        draw.line([(sx, sy), (sx, mid_y)], fill=color, width=2)
        draw.line([(sx, mid_y), (ex, mid_y)], fill=color, width=2)
        draw.line([(ex, mid_y), (ex, ey)], fill=color, width=2)
        draw_arrowhead(draw, ex, ey, ex, mid_y, color)
        _draw_label(draw, sx, sy, ex, ey, label, fb, color)
        return

    # Normal L-shaped route: horizontal then vertical
    draw.line([(sx, sy), (corner_x, sy)], fill=color, width=2)
    draw.line([(corner_x, sy), (corner_x, ey)], fill=color, width=2)
    draw.line([(corner_x, ey), (ex, ey)], fill=color, width=2)
    draw_arrowhead(draw, ex, ey, corner_x, ey, color)
    _draw_label(draw, sx, sy, ex, ey, label, fb, color)


def _draw_label(draw, sx, sy, ex, ey, label, fb, color):
    if not label:
        return
    lx = (sx + ex) // 2
    ly = (sy + ey) // 2 - 10
    w, h = text_size(draw, label, fb)
    draw.rectangle([lx - 2, ly - 1, lx + w + 2, ly + h + 1],
                   fill=(255, 255, 255, 200), outline=color)
    draw.text((lx, ly), label, fill=color, font=fb)


def find_fk_connections(all_tables, join_tables_raw):
    """
    For each FK field in each table, try to find the referenced table
    within the same service. Returns list of:
      (src_table, fk_field_name, dst_table)
    Only includes pairs where dst_table exists in all_tables.
    """
    table_names = {t[0].lower(): t[0] for t in all_tables}
    connections = []

    for tname, fields in all_tables:
        for fname, ftype, marker in fields:
            if marker != "FK":
                continue
            # Derive candidate target name: strip trailing "ID" and "s"
            candidate = fname
            if candidate.endswith("ID"):
                candidate = candidate[:-2]
            elif candidate.endswith("Id"):
                candidate = candidate[:-2]

            # Try exact match, then with common singularization
            for key in [candidate.lower(), candidate.lower().rstrip("s")]:
                if key in table_names and table_names[key] != tname:
                    connections.append((tname, fname, table_names[key]))
                    break

    # Also add join-table implicit connections
    for src, field, jt_name in join_tables_raw:
        jt_lower = jt_name.lower()
        if jt_lower in table_names:
            connections.append((src, f"many2many:{field}", table_names[jt_lower]))

    return connections


def generate_diagram(service_name, db_name, tables, join_tables_raw):
    if not tables:
        return

    ft, fh, fb = load_fonts()

    # Measure tables
    tmp = Image.new("RGB", (1, 1))
    tmp_d = ImageDraw.Draw(tmp)

    jt_data = []
    seen_jt = set()
    for src, field, jt_name in join_tables_raw:
        if jt_name not in seen_jt:
            seen_jt.add(jt_name)
            ref = field.rstrip("s").capitalize()
            jt_fields = [
                (f"{src.lower()}_id",  "uint64", "FK"),
                (f"{ref.lower()}_id", "uint64",  "FK"),
            ]
            jt_data.append((jt_name, jt_fields))

    all_tables   = tables + jt_data
    join_flags   = [False] * len(tables) + [True] * len(jt_data)
    sizes        = [measure_table(tmp_d, n, f, fh, fb) for n, f in all_tables]

    # Grid layout: up to 3 columns
    MARGIN = 50
    GAP_X  = 70
    GAP_Y  = 60
    COLS   = min(3, len(all_tables)) if len(all_tables) > 2 else len(all_tables)

    col_widths = [0] * COLS
    for i, (w, h) in enumerate(sizes):
        col_widths[i % COLS] = max(col_widths[i % COLS], w)

    row_heights = []
    for r in range(0, len(all_tables), COLS):
        row_heights.append(max(sizes[r:r+COLS], key=lambda x: x[1])[1])

    canvas_w = sum(col_widths) + GAP_X * (COLS - 1) + MARGIN * 2
    canvas_h = sum(row_heights) + GAP_Y * (len(row_heights) - 1) + MARGIN * 2 + 55

    title = f"{service_name}  ·  {db_name}"
    tw, _ = text_size(tmp_d, title, ft)
    canvas_w = max(canvas_w, tw + MARGIN * 2)

    img  = Image.new("RGB", (canvas_w, canvas_h), BG_COLOR)
    draw = ImageDraw.Draw(img)

    # Subtle grid background
    for gx in range(0, canvas_w, 30):
        draw.line([(gx, 0), (gx, canvas_h)], fill=GRID_COLOR, width=1)
    for gy in range(0, canvas_h, 30):
        draw.line([(0, gy), (canvas_w, gy)], fill=GRID_COLOR, width=1)

    # Title bar
    draw.rectangle([0, 0, canvas_w, 44], fill=TABLE_HEADER_BG)
    draw.text((MARGIN, 11), title, fill=(255, 255, 255), font=ft)

    # Draw tables and collect positions
    positions    = {}   # table_name → (x, y, w, h)
    field_rows   = {}   # table_name → {field_name: (left_x, right_x, center_y)}

    cur_y = 55 + MARGIN
    for row_idx, rh in enumerate(row_heights):
        cur_x = MARGIN
        for col_idx in range(COLS):
            ti = row_idx * COLS + col_idx
            if ti >= len(all_tables):
                break
            tname, tfields = all_tables[ti]
            is_join = join_flags[ti]
            w, h, fcenters = draw_table(draw, cur_x, cur_y, tname, tfields, fh, fb, is_join)
            positions[tname]  = (cur_x, cur_y, w, h)
            field_rows[tname] = fcenters
            cur_x += col_widths[col_idx] + GAP_X
        cur_y += rh + GAP_Y

    # Draw FK connections
    connections = find_fk_connections(all_tables, join_tables_raw)
    drawn = set()

    for src_table, fk_field, dst_table in connections:
        key = (src_table, fk_field, dst_table)
        if key in drawn or src_table not in positions or dst_table not in positions:
            continue
        drawn.add(key)

        sx_l, sx_r, s_cy = field_rows[src_table].get(fk_field, (0, 0, 0))
        if s_cy == 0:
            # join table connection: use center of source header
            tx, ty, tw2, th2 = positions[src_table]
            s_cy = ty + ROW_H // 2
            sx_l, sx_r = tx, tx + tw2

        tx, ty, tw2, th2 = positions[dst_table]

        # Exit from right or left edge depending on which side target is on
        if tx >= sx_r:
            start_x = sx_r
        else:
            start_x = sx_l

        label = fk_field if not fk_field.startswith("many2many:") else ""
        draw_fk_connector(draw, start_x, s_cy, tx, ty, tw2, th2,
                          RELATION_COLOR, label, fb)

    # Legend
    leg_x = MARGIN
    leg_y = canvas_h - 22
    for marker, color, label in [
        ("PK", PK_COLOR,   "Primary Key"),
        ("FK", FK_COLOR,   "Foreign Key"),
        ("UK", UK_COLOR,   "Unique"),
        ("→",  RELATION_COLOR, "FK Relationship"),
    ]:
        draw.text((leg_x, leg_y), f"{marker}  {label}", fill=color, font=fb)
        leg_x += text_size(draw, f"{marker}  {label}", fb)[0] + 30

    out = os.path.join(OUTPUT_DIR, f"{service_name.replace('-', '_')}_db.png")
    img.save(out, "PNG")
    print(f"  Generated: {out}")


def main():
    print("Generating database diagrams...")
    for service, db_name in SERVICES:
        model_dir = os.path.join(BASE_DIR, service, "internal", "model")
        if not os.path.isdir(model_dir):
            print(f"  Skipping {service}: no model directory")
            continue
        tables, join_tables = parse_go_models(model_dir)
        if not tables:
            print(f"  Skipping {service}: no models found")
            continue
        print(f"  {service}: {len(tables)} tables, {len(join_tables)} join tables")
        generate_diagram(service, db_name, tables, join_tables)
    print("Done!")


if __name__ == "__main__":
    main()
