"""Geometry check: the Go label renderer against bin/nfc-music-labels.

1. GOLDEN_DIR=<dir> go test ./internal/labels -run TestGoldenRender
   writes <dir>/cases.json, the art JPEGs and one Go PDF per case.
2. uv run --with pymupdf --with numpy python -I docs/reference/golden.py <dir> <nfc-music-labels>
   renders the same cases with the script (HOME=<dir>/home, so it reads no
   saved offsets and writes its cache there) and compares, per slot:
   - art rectangles (PDF image placement) and dark fill (bleed) rectangles,
     read from the PDF drawing commands, exact;
   - a raster cross-check at 600 dpi: the box of pixels in each art color.
     The bottom edge is skipped for the full layout, since the fade height
     follows the text and the fonts differ.
It prints the max deviation in mm per case and exits 1 above 0.1 mm.
"""

import json
import os
import subprocess
import sys

import numpy as np
import pymupdf

MM = 72 / 25.4
LIMIT_MM = 0.1
DPI = 600


def vector_boxes(path):
    """Per page: sorted art rects and sorted dark fill rects, in points."""
    pages = []
    for page in pymupdf.open(path):
        art = [tuple(i["bbox"]) for i in page.get_image_info()]
        fills = [tuple(d["rect"]) for d in page.get_drawings()
                 if d.get("fill") is not None and max(d["fill"]) < 0.1 and "f" in d.get("type", "")]
        key = lambda r: (round(r[1], 1), round(r[0], 1))
        pages.append((sorted(art, key=key), sorted(fills, key=key)))
    return pages


def color_boxes(path, colors, tol=25):
    """Per page: for each art color, the pixel box (x0, y0, x1, y1) in points."""
    out = []
    for page in pymupdf.open(path):
        pix = page.get_pixmap(dpi=DPI, colorspace=pymupdf.csRGB, alpha=False)
        img = np.frombuffer(pix.samples, np.uint8).reshape(pix.height, pix.width, 3).astype(np.int16)
        boxes = {}
        for name, rgb in colors.items():
            ys, xs = np.nonzero((np.abs(img - np.array(rgb)) < tol).all(axis=2))
            if len(xs):
                k = 72 / DPI
                boxes[name] = (xs.min() * k, ys.min() * k, (xs.max() + 1) * k, (ys.max() + 1) * k)
        out.append(boxes)
    return out


def max_dev(a, b, skip_bottom=False):
    idx = (0, 1, 2) if skip_bottom else (0, 1, 2, 3)
    return max(abs(a[i] - b[i]) for i in idx) / MM


def compare(case, cfg, outdir, script):
    go_pdf = os.path.join(outdir, case["name"] + "-go.pdf")
    py_pdf = os.path.join(outdir, case["name"] + "-py.pdf")
    args = [script, "--no-map", "-y", "-o", py_pdf, "--template", case["script"], "--background", case["background"],
            "--start", f'{case["start_row"]},{case["start_col"]}', "--offset-x", str(case["offset_x"]),
            "--offset-y", str(case["offset_y"]), "--inset", str(cfg["inset_mm"]), "--bleed", str(cfg["bleed_mm"]),
            "--edge-bleed", str(cfg["edge_bleed_mm"])]
    for it in cfg["items"]:
        args += ["--image", f'{it["name"]}={os.path.join(outdir, it["file"])}']
    args += [it["name"] for it in cfg["items"]]
    env = dict(os.environ, HOME=os.path.join(outdir, "home"))
    subprocess.run(args, check=True, env=env, stderr=subprocess.DEVNULL)

    go, py = vector_boxes(go_pdf), vector_boxes(py_pdf)
    if len(go) != len(py):
        return f"{len(go)} pages vs {len(py)}", float("inf"), float("inf"), float("inf"), []
    art = fill = 0.0
    fills = []
    for (ga, gf), (pa, pf) in zip(go, py):
        if len(ga) != len(pa) or len(gf) != len(pf):
            return f"art {len(ga)}/{len(pa)}, fills {len(gf)}/{len(pf)}", float("inf"), float("inf"), float("inf"), []
        art = max([art] + [max_dev(g, p) for g, p in zip(ga, pa)])
        fill = max([fill] + [max_dev(g, p) for g, p in zip(gf, pf)])
        fills += [round((p[3] - p[1]) / MM - 25.4, 2) for p in pf]  # height minus the label: top + bottom bleed

    colors = {it["file"]: tuple(int(it["rgb"][i:i + 2], 16) for i in (0, 2, 4)) for it in cfg["items"]}
    full = case["script"] == "5434"
    raster = 0.0
    for gb, pb in zip(color_boxes(go_pdf, colors), color_boxes(py_pdf, colors)):
        if gb.keys() != pb.keys():
            return f"art colors {sorted(gb)} vs {sorted(pb)}", art, fill, float("inf"), fills
        raster = max([raster] + [max_dev(gb[k], pb[k], skip_bottom=full) for k in gb])
    return "", art, fill, raster, fills


def main():
    outdir, script = sys.argv[1], sys.argv[2]
    with open(os.path.join(outdir, "cases.json")) as f:
        cfg = json.load(f)
    os.makedirs(os.path.join(outdir, "home"), exist_ok=True)
    worst = 0.0
    print(f'{"case":18} {"art mm":>8} {"fill mm":>8} {"raster mm":>10}  fill bleed (top+bottom, mm)')
    for case in cfg["cases"]:
        err, art, fill, raster, fills = compare(case, cfg, outdir, script)
        worst = max(worst, art, fill, raster)
        print(f'{case["name"]:18} {art:8.4f} {fill:8.4f} {raster:10.4f}  {fills} {err}')
    print(f"max deviation {worst:.4f} mm (limit {LIMIT_MM})")
    sys.exit(0 if worst <= LIMIT_MM else 1)


if __name__ == "__main__":
    main()
