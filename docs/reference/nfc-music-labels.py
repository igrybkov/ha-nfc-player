#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["reportlab>=4", "pillow>=10", "pyyaml>=6", "questionary>=2"]
# ///
"""Print album/artist art labels for NFC music tags on Avery label sheets.

Templates (--template):
  5260  Letter sheet, 30 labels of 2-5/8" x 1" (3 columns x 10 rows). Default.
  5434  4" x 6" sheet, 10 labels of 1-1/2" x 1" (2 columns x 5 rows).

By default, labels come from the "Music Map" (music_urls) in automations.yaml.
Run in a terminal without --only, --start, --skip or names, and it asks which
labels to print and where on the sheet to start. Pass --yes to skip the prompts.
Artwork comes from Apple Music: the iTunes lookup API for album URLs, the
page's og:image for playlists and artists, and iTunes search for names that
have no URL.

Examples:
  bin/nfc-music-labels                              # every tag in the Music Map
  bin/nfc-music-labels -o labels.pdf --only Queen   # names that contain "Queen"
  bin/nfc-music-labels "Muse — Origin of Symmetry"  # extra names, art by search
  bin/nfc-music-labels --template 5434              # small 4x6 sheets
  bin/nfc-music-labels --start 4,2 --yes            # start at row 4, column 2
  bin/nfc-music-labels --image "Volodymyr Ivasiuk=~/Pictures/ivasiuk.jpg"
  bin/nfc-music-labels --outline                    # test print on plain paper
  bin/nfc-music-labels --offset-y -1 --save-offsets # print 1 mm lower from now on
"""

import argparse
import hashlib
import subprocess
from dataclasses import dataclass
import html
import json
import re
import sys
import urllib.parse
import urllib.request
from pathlib import Path

import yaml
from PIL import Image
from reportlab.lib.colors import Color, black, white
from reportlab.lib.units import inch
from reportlab.lib.utils import ImageReader, simpleSplit
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.pdfgen import canvas

REPO = Path(__file__).resolve().parent.parent
BACKGROUNDS = {"white": (white, black), "black": (black, white)}  # background, text
CACHE = Path.home() / ".cache" / "nfc-music-labels"
# Per-template printer calibration, written by --save-offsets.
CONFIG = Path.home() / ".config" / "nfc-music-labels.json"
MM = inch / 25.4



@dataclass(frozen=True)
class Template:
    """Sheet geometry in inches, plus text sizes and style that suit the label."""

    page_w: float
    page_h: float
    label_w: float
    label_h: float
    cols: int
    rows: int
    left: float
    top: float
    col_gap: float
    title_size: float
    subtitle_size: float
    style: str

    @property
    def per_sheet(self) -> int:
        return self.cols * self.rows


TEMPLATES = {
    "5260": Template(8.5, 11, 2.625, 1, 3, 10, 0.1875, 0.5, 0.125, 11, 8.5, "side"),
    "5434": Template(4, 6, 1.5, 1, 2, 5, 0.46875, 0.5, 0.0625, 8, 6.5, "full"),
}

SEPARATOR = "—"  # Tag names look like "Artist — Album".
FONTS = {
    "regular": "/System/Library/Fonts/Supplemental/Arial.ttf",
    "bold": "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
}


class _AnyTagLoader(yaml.SafeLoader):
    """SafeLoader that tolerates HA tags such as !input or !secret."""


_AnyTagLoader.add_multi_constructor("!", lambda loader, suffix, node: None)


def find_music_map(node):
    if isinstance(node, dict):
        if isinstance(node.get("music_urls"), dict):
            return node["music_urls"]
        node = node.values()
    if isinstance(node, (list, type({}.values()))):
        for child in node:
            if found := find_music_map(child):
                return found
    return None


def load_music_map(path: Path) -> dict[str, str | None]:
    music_map = find_music_map(yaml.load(path.read_text(), Loader=_AnyTagLoader)) or {}
    return {
        name: value.get("url") if isinstance(value, dict) else value
        for name, value in music_map.items()
    }


def fetch(url: str) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0 nfc-music-labels"})
    with urllib.request.urlopen(request, timeout=20) as response:
        return response.read()


def itunes_artwork(result: dict) -> str | None:
    art = result.get("artworkUrl100")
    return art and re.sub(r"/\d+x\d+bb\.", "/1200x1200bb.", art)


def album_artwork(url: str) -> str | None:
    if album := re.search(r"/album/(?:[^/]+/)?(\d+)", url):
        data = json.loads(fetch(f"https://itunes.apple.com/lookup?id={album[1]}"))
        return itunes_artwork(data["results"][0]) if data["results"] else None


def page_artwork(url: str) -> str | None:
    page = fetch(url).decode("utf-8", "replace")
    if meta := re.search(r'<meta[^>]+property="og:image"[^>]+content="([^"]+)"', page):
        # og:image is a 1200x630 social card (for "Essentials" playlists, the artist
        # photo with a branded overlay). The last path segment picks the rendition.
        return re.sub(r"/[^/?]+(\?.*)?$", "/1200x1200bb.jpg", html.unescape(meta[1]))


def search_artwork(name: str) -> str | None:
    term = urllib.parse.quote(name.replace(SEPARATOR, " "))
    data = json.loads(fetch(f"https://itunes.apple.com/search?term={term}&entity=album&limit=1"))
    return itunes_artwork(data["results"][0]) if data["results"] else None


def artwork_url(name: str, url: str | None) -> str | None:
    """Try the URL first. Fall back to search, since albums get pulled from Apple Music."""
    lookups = [lambda: album_artwork(url), lambda: page_artwork(url)] if url else []
    for lookup in lookups + [lambda: search_artwork(name)]:
        try:
            if found := lookup():
                return found
        except Exception as error:
            print(f"  {name}: {error}", file=sys.stderr)
    return None


def load_artwork_index(refresh: bool) -> dict[str, str]:
    """Artwork URLs found on earlier runs, keyed by tag name and link."""
    try:
        return {} if refresh else json.loads((CACHE / "artwork.json").read_text())
    except FileNotFoundError:
        return {}


def save_artwork_index(index: dict[str, str]):
    CACHE.mkdir(parents=True, exist_ok=True)
    (CACHE / "artwork.json").write_text(json.dumps(index, indent=2, ensure_ascii=False) + "\n")


def load_image(source: str, refresh: bool = False) -> Image.Image:
    if not source.startswith(("http://", "https://")):
        return Image.open(Path(source).expanduser()).convert("RGB")
    cached = CACHE / (hashlib.sha1(source.encode()).hexdigest() + ".img")
    if refresh or not cached.exists():
        CACHE.mkdir(parents=True, exist_ok=True)
        cached.write_bytes(fetch(source))
    return Image.open(cached).convert("RGB")


def crop_to(image: Image.Image, ratio: float) -> Image.Image:
    w, h = image.size
    if w / h > ratio:
        new_w = round(h * ratio)
        return image.crop(((w - new_w) // 2, 0, (w - new_w) // 2 + new_w, h))
    new_h = round(w / ratio)
    return image.crop((0, (h - new_h) // 2, w, (h - new_h) // 2 + new_h))


def fit_lines(text: str, font: str, size: float, width: float, max_lines: int, min_size: float):
    """Shrink text until it wraps into max_lines; ellipsize as a last resort."""
    def too_wide(line):
        return pdfmetrics.stringWidth(line, font, size) > width

    while True:
        lines = simpleSplit(text, font, size, width)
        if (len(lines) <= max_lines and not any(map(too_wide, lines))) or size <= min_size:
            break
        size -= 0.5
    if len(lines) > max_lines:
        lines = lines[:max_lines]
        lines[-1] += "…"
    for i, line in enumerate(lines):
        if too_wide(line):
            while line and too_wide(line + "…"):
                line = line[:-1]
            lines[i] = line.rstrip(" …") + "…"
    return lines, size


def draw_text_block(c, title: str, subtitle: str, x, y, width, color, sizes, draw=True):
    """Draw title (bold) over subtitle, bottom-aligned at y. Returns the block height."""
    title_size, sub_size = sizes
    sub_lines, sub_size = fit_lines(subtitle, "regular", sub_size, width, 2, 4.5) if subtitle else ([], 0)
    title_lines, title_size = fit_lines(title, "bold", title_size, width, 2, 5)
    c.setFillColor(color)
    cursor = y
    for font, lines, size in (("regular", sub_lines, sub_size), ("bold", title_lines, title_size)):
        for line in reversed(lines):
            if draw:
                c.setFont(font, size)
                c.drawString(x, cursor + size * 0.2, line)
            cursor += size * 1.15
    return cursor - y


def draw_label(c, x, y, w, h, name: str, image: Image.Image | None, style: str, sizes, fg, dpi: int):
    artist, _, album = (part.strip() for part in name.partition(SEPARATOR))
    pad = 0.06 * inch

    if not image:
        draw_text_block(c, artist, album, x + pad, y + pad, w - 2 * pad, fg, sizes)
        return

    if style == "full":
        px_w, px_h = round(w / inch * dpi), round(h / inch * dpi)
        art = crop_to(image, w / h).resize((px_w, px_h), Image.LANCZOS)
        # Fade to black under the text so it reads on any cover.
        text_h = draw_text_block(c, artist, album, x + pad, y, w - 2 * pad, white, sizes, draw=False)
        band = min(px_h, round((text_h + 2.5 * pad) / h * px_h))
        fade = Image.new("L", (1, px_h), 0)
        fade.putdata([0] * (px_h - band) + [round(190 * (i / band) ** 0.8) for i in range(band)])
        art.paste(Image.new("RGB", art.size, "black"), mask=fade.resize(art.size))
        c.drawImage(ImageReader(art), x, y, w, h)
        draw_text_block(c, artist, album, x + pad, y + pad, w - 2 * pad, white, sizes)
        return

    # "side" style: square art on the left, text on the right, centered vertically.
    art = crop_to(image, 1).resize((round(h / inch * dpi),) * 2, Image.LANCZOS)
    c.drawImage(ImageReader(art), x, y, h, h)
    text_x, text_w = x + h + 1.5 * pad, w - h - 2.5 * pad
    text_h = draw_text_block(c, artist, album, text_x, y, text_w, fg, sizes, draw=False)
    draw_text_block(c, artist, album, text_x, y + (h - text_h) / 2, text_w, fg, sizes)


def sheet_map(t: Template, skip: int, count: int) -> str:
    """Text picture of the first sheet: · used already, ■ new label, ○ left free."""
    lines = ["     " + "".join(f"{col:<3}" for col in range(1, t.cols + 1))]
    for row in range(t.rows):
        marks = []
        for col in range(t.cols):
            slot = row * t.cols + col
            marks.append("·" if slot < skip else "■" if slot < skip + count else "○")
        lines.append(f"  {row + 1:>2} " + "  ".join(marks))
    sheets = -(-(skip + count) // t.per_sheet)
    more = f"\n  (+{sheets - 1} more sheet(s), starting at row 1)" if sheets > 1 else ""
    return "\n".join(lines) + "\n  · used   ■ printed   ○ left free" + more


def parse_start(value: str, t: Template) -> int:
    """'ROW,COL' (1-based) to the number of slots to skip."""
    row, col = (int(part) for part in value.split(","))
    if not (1 <= row <= t.rows and 1 <= col <= t.cols):
        sys.exit(f"--start must be within rows 1-{t.rows} and columns 1-{t.cols}.")
    return (row - 1) * t.cols + (col - 1)


def ask(question):
    """Run a questionary prompt; Ctrl-C quits."""
    if (answer := question.ask()) is None:
        sys.exit("Cancelled.")
    return answer


def fuzzy_match(query: str, text: str) -> bool:
    """True when the query letters appear in order in text: "mess" finds "Metallica — Essential"."""
    remaining = iter(text.lower())
    return all(char in remaining for char in query.lower())


def use_fuzzy_search():
    """Swap questionary's substring search filter for fuzzy_match."""
    from questionary.prompts.common import InquirerControl

    def filtered_choices(self):
        if not self.search_filter:
            self.found_in_search = False
            return self.choices
        found = [choice for choice in self.choices if fuzzy_match(self.search_filter, choice.title)]
        self.found_in_search = bool(found)
        return found or self.choices  # questionary needs a non-empty list; the query turns red

    InquirerControl.filtered_choices = property(filtered_choices)


def ask_number(question: str, count: int) -> int:
    """Pick 1..count with arrows, or type to search. Choices stay in numeric order."""
    import questionary

    return int(ask(questionary.select(
        f"{question} (1-{count}):",
        choices=[str(n) for n in range(1, count + 1)],
        use_search_filter=True,
        use_jk_keys=False,
    )))


def choose_interactively(entries: dict, t: Template, copies: int, background: str) -> tuple[dict, int, int, str]:
    import questionary

    use_fuzzy_search()
    names = ask(questionary.checkbox(
        "Labels to print:",
        choices=[questionary.Choice(name, checked=False) for name in entries],
        use_search_filter=True,
        use_jk_keys=False,
        validate=lambda chosen: bool(chosen) or "Pick at least one label.",
    ))
    copies = int(ask(questionary.text(
        "Copies of each:", default=str(copies), validate=lambda v: v.isdigit() and int(v) > 0 or "Enter 1 or more.",
    )))
    background = ask(questionary.select("Background:", choices=list(BACKGROUNDS), default=background))
    while True:
        row = ask_number("Start at row", t.rows)
        col = ask_number("Start at column", t.cols)
        skip = (row - 1) * t.cols + (col - 1)
        print(sheet_map(t, skip, len(names) * copies))
        if ask(questionary.confirm("Print this?", default=True)):
            return {name: entries[name] for name in names}, skip, copies, background


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("names", nargs="*", help='extra tag names, "Artist — Album" (art found by search)')
    parser.add_argument("-o", "--output", default="nfc-music-labels.pdf", help="output PDF path")
    parser.add_argument("--automations", type=Path, default=REPO / "automations.yaml")
    parser.add_argument("--no-map", action="store_true", help="skip the Music Map, use only the given names")
    parser.add_argument("--only", action="append", default=[], help="keep names containing this text (repeatable)")
    parser.add_argument("--image", action="append", default=[], metavar="NAME=PATH_OR_URL",
                        help="override artwork for one name (repeatable)")
    parser.add_argument("--template", choices=TEMPLATES, default="5260", help="Avery template (default: 5260)")
    parser.add_argument("--style", choices=["full", "side"],
                        help="full: art fills the label; side: square art + text (default: per template)")
    parser.add_argument("--background", choices=BACKGROUNDS, default="white",
                        help="label background, to match the card it goes on (default: white)")
    parser.add_argument("--copies", type=int, default=1, help="copies of each label")
    parser.add_argument("--start", metavar="ROW,COL", help="first free position on the sheet, e.g. 4,2")
    parser.add_argument("--skip", type=int, default=0, help="leave the first N label slots empty")
    parser.add_argument("-y", "--yes", action="store_true", help="no prompts")
    parser.add_argument("--offset-x", type=float, help="shift everything right, mm (default: saved, else 0)")
    parser.add_argument("--offset-y", type=float, help="shift everything up, mm (default: saved, else 0)")
    parser.add_argument("--save-offsets", action="store_true", help="remember the offsets for this template")
    parser.add_argument("--bleed", type=float, default=1.5,
                        help="extend a dark background past the label edge, mm, so drift leaves no white strip")
    parser.add_argument("--edge-bleed", type=float, default=0.4,
                        help="smaller bleed onto a free label below, mm; covers drift, marks its top edge slightly")
    parser.add_argument("--inset", type=float, default=1.5,
                        help="keep art this far inside the label edge, mm, so printer drift stays on the sticker")
    parser.add_argument("--outline", action="store_true", help="draw label outlines for a test print")
    parser.add_argument("--refresh", action="store_true", help="ignore cached artwork and download it again")
    parser.add_argument("--dpi", type=int, default=600)
    args = parser.parse_args()

    for key, path in FONTS.items():
        pdfmetrics.registerFont(TTFont(key, path))

    entries = {} if args.no_map else load_music_map(args.automations)
    entries |= {name: None for name in args.names}
    if args.only:
        entries = {n: u for n, u in entries.items() if any(o.lower() in n.lower() for o in args.only)}
    overrides = dict(item.split("=", 1) for item in args.image)
    if not entries:
        sys.exit("No labels to print.")

    t = TEMPLATES[args.template]
    try:
        config = json.loads(CONFIG.read_text())
    except FileNotFoundError:
        config = {}
    saved = config.get(args.template, {})
    offset_x = args.offset_x if args.offset_x is not None else saved.get("offset_x_mm", 0)
    offset_y = args.offset_y if args.offset_y is not None else saved.get("offset_y_mm", 0)
    if args.save_offsets:
        config[args.template] = {"offset_x_mm": offset_x, "offset_y_mm": offset_y}
        CONFIG.parent.mkdir(parents=True, exist_ok=True)
        CONFIG.write_text(json.dumps(config, indent=2) + "\n")
        print(f"Saved offsets for {args.template}: x {offset_x:+g} mm, y {offset_y:+g} mm", file=sys.stderr)
    skip = parse_start(args.start, t) if args.start else args.skip
    copies, background = args.copies, args.background
    interactive = sys.stdin.isatty() and not (args.yes or args.only or args.names or args.start or args.skip)
    if interactive:
        entries, skip, copies, background = choose_interactively(entries, t, copies, background)

    labels = []
    artwork_index = load_artwork_index(args.refresh)
    for name, url in entries.items():
        try:
            key = f"{name}\n{url or ''}"
            source = overrides.get(name) or artwork_index.get(key) or artwork_url(name, url)
            if source and name not in overrides:
                artwork_index[key] = source
            image = load_image(source, args.refresh) if source else None
        except Exception as error:  # Keep going; a text-only label is still useful.
            print(f"  {name}: {error}", file=sys.stderr)
            image = None
        print(f"{'✓' if image else '✗'} {name}", file=sys.stderr)
        labels += [(name, image)] * copies

    bg, fg = BACKGROUNDS[background]
    save_artwork_index(artwork_index)
    style = args.style or t.style
    sizes = (t.title_size, t.subtitle_size)
    c = canvas.Canvas(args.output, pagesize=(t.page_w * inch, t.page_h * inch))
    slots = [None] * skip + labels
    for start in range(0, len(slots), t.per_sheet):
        page = slots[start : start + t.per_sheet]
        for slot, label in enumerate(page):
            row, col = divmod(slot, t.cols)
            x = (t.left + col * (t.label_w + t.col_gap)) * inch + offset_x * MM
            y = (t.page_h - t.top - (row + 1) * t.label_h) * inch + offset_y * MM
            w, h = t.label_w * inch, t.label_h * inch
            if label:
                if bg != white:
                    # Bleed past the edge, so printer drift leaves no white strip. Columns have
                    # a gap, so bleed sideways always. Rows touch, so bleed fully down only onto
                    # a label printed in this run or the sheet margin; a free label below gets
                    # just the small edge bleed. Labels above are always printed or used already.
                    bleed = args.bleed * MM
                    below_ok = row == t.rows - 1 or slot + t.cols < len(page)
                    bottom = bleed if below_ok else args.edge_bleed * MM
                    c.setFillColor(bg)
                    c.rect(x - bleed, y - bottom, w + 2 * bleed, h + bleed + bottom, stroke=0, fill=1)
                inset = args.inset * MM
                c.saveState()
                path = c.beginPath()
                path.roundRect(x + inset, y + inset, w - 2 * inset, h - 2 * inset, 0.04 * inch)
                c.clipPath(path, stroke=0, fill=0)
                draw_label(c, x + inset, y + inset, w - 2 * inset, h - 2 * inset, *label, style, sizes, fg, args.dpi)
                c.restoreState()
            if args.outline:
                c.setStrokeColor(Color(0.6, 0.6, 0.6))
                c.setLineWidth(0.3)
                c.roundRect(x, y, w, h, 0.04 * inch, stroke=1, fill=0)
        c.showPage()
    c.save()
    sheets = -(-len(slots) // t.per_sheet)
    print(f"Wrote {args.output}: {len(labels)} labels on {sheets} sheet(s). Print at 100% (no scaling).",
          file=sys.stderr)
    if interactive and sys.platform == "darwin":
        import questionary

        if ask(questionary.confirm("Open the PDF?", default=True)):
            subprocess.run(["open", args.output], check=False)


if __name__ == "__main__":
    main()
