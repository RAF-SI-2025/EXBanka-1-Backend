#!/usr/bin/env python3
"""
Generate database ER diagrams for each service from GORM model Go files.
Uses PIL/Pillow only (no external dependencies needed).
"""

import os
import re
import glob
from PIL import Image, ImageDraw, ImageFont

BASE_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUTPUT_DIR = os.path.dirname(os.path.abspath(__file__))

# Services and their model directories
SERVICES = [
    ("auth-service", "auth_db"),
    ("user-service", "user_db"),
    ("client-service", "client_db"),
    ("account-service", "account_db"),
    ("card-service", "card_db"),
    ("transaction-service", "transaction_db"),
    ("credit-service", "credit_db"),
    ("exchange-service", "exchange_db"),
    ("verification-service", "verification_db"),
    ("notification-service", "notification_db"),
    ("stock-service", "stock_db"),
]

# Colors
BG_COLOR = (255, 255, 255)
TABLE_HEADER_BG = (66, 103, 178)
TABLE_HEADER_FG = (255, 255, 255)
TABLE_BODY_BG = (245, 247, 250)
TABLE_BORDER = (180, 190, 205)
TEXT_COLOR = (40, 40, 40)
FK_COLOR = (200, 80, 80)
PK_COLOR = (66, 133, 66)
RELATION_COLOR = (150, 80, 80)
JOIN_TABLE_BG = (255, 243, 224)
JOIN_TABLE_HEADER_BG = (230, 160, 60)


def parse_go_models(model_dir):
    """Parse Go struct definitions from model files."""
    tables = []
    relationships = []
    join_tables = []

    go_files = glob.glob(os.path.join(model_dir, "*.go"))
    for filepath in sorted(go_files):
        with open(filepath, "r") as f:
            content = f.read()

        # Find all struct definitions
        struct_pattern = re.compile(
            r'type\s+(\w+)\s+struct\s*\{([^}]*(?:\{[^}]*\}[^}]*)*)\}',
            re.DOTALL
        )
        for match in struct_pattern.finditer(content):
            struct_name = match.group(1)
            body = match.group(2)

            # Skip non-model structs (no gorm tags, no ID field typically)
            if "gorm:" not in body and "ID" not in body:
                continue

            fields = []
            for line in body.strip().split("\n"):
                line = line.strip()
                if not line or line.startswith("//") or line.startswith("*"):
                    continue

                # Parse field: Name Type `tags`
                field_match = re.match(
                    r'(\w+)\s+([\w.*\[\]]+(?:\.\w+)?)\s*(?:`([^`]*)`)?',
                    line
                )
                if not field_match:
                    continue

                field_name = field_match.group(1)
                field_type = field_match.group(2)
                tags = field_match.group(3) or ""

                # Skip embedded structs like gorm.Model
                if field_name in ("gorm", "Model"):
                    continue

                # Detect keys
                is_pk = "primaryKey" in tags or field_name == "ID"
                is_unique = "uniqueIndex" in tags or "unique" in tags
                is_fk = False

                # Detect foreign keys from gorm tags
                fk_match = re.search(r'foreignKey:(\w+)', tags)
                m2m_match = re.search(r'many2many:(\w+)', tags)

                if fk_match:
                    is_fk = True
                    relationships.append((struct_name, field_name, fk_match.group(1)))

                if m2m_match:
                    join_tables.append((struct_name, field_name, m2m_match.group(1)))

                # Skip slice/pointer-to-slice fields (they are relationship fields, not columns)
                if field_type.startswith("[]") or (field_type.startswith("*") and "[]" in field_type):
                    continue
                if field_type in ("Roles", "Permissions", "AdditionalPermissions"):
                    continue

                # Clean up type for display
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
                elif is_fk or field_name.endswith("ID") and field_name != "ID":
                    marker = "FK"

                fields.append((field_name, display_type, marker))

            if fields:
                tables.append((struct_name, fields))

    return tables, relationships, join_tables


def get_text_size(draw, text, font):
    """Get text bounding box width and height."""
    bbox = draw.textbbox((0, 0), text, font=font)
    return bbox[2] - bbox[0], bbox[3] - bbox[1]


def draw_table(draw, x, y, table_name, fields, font_header, font_body, is_join=False):
    """Draw a single table and return its bounding box (x, y, w, h)."""
    padding = 8
    row_height = 22
    marker_width = 30

    # Calculate column widths
    max_name_w = 0
    max_type_w = 0
    for name, ftype, marker in fields:
        nw, _ = get_text_size(draw, name, font_body)
        tw, _ = get_text_size(draw, ftype, font_body)
        max_name_w = max(max_name_w, nw)
        max_type_w = max(max_type_w, tw)

    header_w, _ = get_text_size(draw, table_name, font_header)
    col_width = max(max_name_w + max_type_w + marker_width + padding * 4, header_w + padding * 2)
    table_height = row_height + len(fields) * row_height

    # Header
    header_bg = JOIN_TABLE_HEADER_BG if is_join else TABLE_HEADER_BG
    draw.rectangle([x, y, x + col_width, y + row_height], fill=header_bg, outline=TABLE_BORDER)
    draw.text((x + padding, y + 3), table_name, fill=TABLE_HEADER_FG, font=font_header)

    # Body
    body_bg = JOIN_TABLE_BG if is_join else TABLE_BODY_BG
    for i, (name, ftype, marker) in enumerate(fields):
        ry = y + row_height + i * row_height
        draw.rectangle([x, ry, x + col_width, ry + row_height], fill=body_bg, outline=TABLE_BORDER)

        # Marker
        if marker == "PK":
            draw.text((x + 4, ry + 3), "PK", fill=PK_COLOR, font=font_body)
        elif marker == "FK":
            draw.text((x + 4, ry + 3), "FK", fill=FK_COLOR, font=font_body)
        elif marker == "UK":
            draw.text((x + 4, ry + 3), "UK", fill=(0, 100, 180), font=font_body)

        # Field name and type
        draw.text((x + marker_width, ry + 3), name, fill=TEXT_COLOR, font=font_body)
        draw.text((x + marker_width + max_name_w + padding * 2, ry + 3), ftype, fill=(120, 120, 120), font=font_body)

    return col_width, table_height


def generate_diagram(service_name, db_name, tables, join_tables):
    """Generate a PNG diagram for a service's database."""
    if not tables:
        return

    # Try to load a monospace font
    font_header = None
    font_body = None
    font_title = None
    for font_path in [
        "/System/Library/Fonts/SFNSMono.ttf",
        "/System/Library/Fonts/Menlo.ttc",
        "/System/Library/Fonts/Monaco.ttf",
        "/Library/Fonts/Courier New.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
    ]:
        if os.path.exists(font_path):
            try:
                font_header = ImageFont.truetype(font_path, 14)
                font_body = ImageFont.truetype(font_path, 12)
                font_title = ImageFont.truetype(font_path, 18)
                break
            except Exception:
                continue

    if font_header is None:
        font_header = ImageFont.load_default()
        font_body = ImageFont.load_default()
        font_title = ImageFont.load_default()

    # Layout: arrange tables in a grid
    # First pass: measure all tables
    tmp_img = Image.new("RGB", (1, 1))
    tmp_draw = ImageDraw.Draw(tmp_img)

    table_sizes = []
    for tname, fields in tables:
        w, h = draw_table(tmp_draw, 0, 0, tname, fields, font_header, font_body)
        table_sizes.append((w, h))

    # Add join tables
    jt_data = []
    for src, field, jt_name in join_tables:
        jt_fields = [
            (f"{src.lower()}_id", "uint64", "FK"),
            (f"{field.lower()}_id", "uint64", "FK"),
        ]
        jt_data.append((jt_name, jt_fields))
        w, h = draw_table(tmp_draw, 0, 0, jt_name, jt_fields, font_header, font_body, is_join=True)
        table_sizes.append((w, h))

    all_tables = tables + jt_data
    all_join_flags = [False] * len(tables) + [True] * len(jt_data)

    # Grid layout
    margin = 40
    gap_x = 50
    gap_y = 40
    cols = min(3, len(all_tables))
    if len(all_tables) <= 2:
        cols = len(all_tables)

    # Calculate row heights and column widths
    rows_data = []
    for i in range(0, len(all_tables), cols):
        row_slice = table_sizes[i:i+cols]
        row_h = max(h for _, h in row_slice)
        rows_data.append(row_h)

    col_widths = [0] * cols
    for i, (w, h) in enumerate(table_sizes):
        c = i % cols
        col_widths[c] = max(col_widths[c], w)

    total_w = sum(col_widths) + gap_x * (cols - 1) + margin * 2
    total_h = sum(rows_data) + gap_y * (len(rows_data) - 1) + margin * 2 + 50  # 50 for title

    # Title sizing
    title_text = f"{service_name} ({db_name})"
    tw, th = get_text_size(tmp_draw, title_text, font_title)
    total_w = max(total_w, tw + margin * 2)

    img = Image.new("RGB", (total_w, total_h), BG_COLOR)
    draw = ImageDraw.Draw(img)

    # Title
    draw.text((margin, 15), title_text, fill=TABLE_HEADER_BG, font=font_title)

    # Draw tables
    table_positions = {}
    cur_y = 50 + margin
    for row_idx, row_h in enumerate(rows_data):
        cur_x = margin
        for col_idx in range(cols):
            ti = row_idx * cols + col_idx
            if ti >= len(all_tables):
                break
            tname, fields = all_tables[ti]
            is_join = all_join_flags[ti]
            w, h = draw_table(draw, cur_x, cur_y, tname, fields, font_header, font_body, is_join)
            table_positions[tname] = (cur_x, cur_y, w, h)
            cur_x += col_widths[col_idx] + gap_x
        cur_y += row_h + gap_y

    # Draw simple relationship lines for FK fields
    # Connect tables where field names suggest relationships
    drawn_relations = set()
    for tname, fields in tables:
        for fname, ftype, marker in fields:
            if marker == "FK" and fname.endswith("ID"):
                # Guess target table from field name
                ref_name = fname[:-2]  # Remove "ID"
                # Try to find matching table
                for other_name, _ in all_tables:
                    if other_name.lower() == ref_name.lower() or other_name.lower().endswith(ref_name.lower()):
                        if tname in table_positions and other_name in table_positions:
                            key = tuple(sorted([tname, other_name]))
                            if key not in drawn_relations:
                                drawn_relations.add(key)
                                sx, sy, sw, sh = table_positions[tname]
                                tx, ty, tw2, th2 = table_positions[other_name]
                                # Draw from right edge of source to left edge of target (or closest edges)
                                s_cx = sx + sw
                                s_cy = sy + sh // 2
                                t_cx = tx
                                t_cy = ty + th2 // 2
                                # If target is to the left, swap edges
                                if tx + tw2 < sx:
                                    s_cx = sx
                                    t_cx = tx + tw2
                                # If same column, use bottom/top
                                if abs(sx - tx) < sw:
                                    s_cx = sx + sw // 2
                                    t_cx = tx + tw2 // 2
                                    if sy < ty:
                                        s_cy = sy + sh
                                        t_cy = ty
                                    else:
                                        s_cy = sy
                                        t_cy = ty + th2
                                draw.line([(s_cx, s_cy), (t_cx, t_cy)], fill=RELATION_COLOR, width=2)
                        break

    # Draw join table connections
    for src, field, jt_name in join_tables:
        if jt_name in table_positions and src in table_positions:
            sx, sy, sw, sh = table_positions[src]
            jx, jy, jw, jh = table_positions[jt_name]
            draw.line(
                [(sx + sw // 2, sy + sh), (jx + jw // 2, jy)],
                fill=(230, 160, 60), width=2
            )

    # Save
    output_path = os.path.join(OUTPUT_DIR, f"{service_name.replace('-', '_')}_db.png")
    img.save(output_path, "PNG")
    print(f"  Generated: {output_path}")


def main():
    print("Generating database diagrams...")
    for service, db_name in SERVICES:
        model_dir = os.path.join(BASE_DIR, service, "internal", "model")
        if not os.path.isdir(model_dir):
            print(f"  Skipping {service}: no model directory")
            continue

        tables, relationships, join_tables = parse_go_models(model_dir)
        if not tables:
            print(f"  Skipping {service}: no models found")
            continue

        print(f"  {service}: {len(tables)} tables, {len(join_tables)} join tables")
        generate_diagram(service, db_name, tables, join_tables)

    print("Done!")


if __name__ == "__main__":
    main()
