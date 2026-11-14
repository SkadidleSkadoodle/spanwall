"""Generate golden test data from Superpaper's span math.

The functions below are copied from Superpaper 2.2.1
(superpaper/wallpaper_processing.py, MIT, see NOTICE) with logging removed,
so Spanwall's Go port can be checked against the original behaviour.

Run from the repo root:
    python tools/golden/superpaper_ref.py
Writes testdata/golden.json and testdata/golden/*.png.
"""
import json
import math
import os
from operator import itemgetter

from PIL import Image, ImageDraw, ImageFilter

OUT_DIR = os.path.join("testdata", "golden")


# ---- copied from Superpaper (MIT) -------------------------------------------

def compute_ppi(res, phys_mm):
    return res[0] / phys_mm[0] * 25.4


def compute_ppinorm_resolutions(resolutions, ppis):
    max_ppi = max(ppis)
    out = []
    for res, ppi in zip(resolutions, ppis):
        r_ppi = ppi / max_ppi
        out.append((round(res[0] / r_ppi), round(res[1] / r_ppi)))
    return out


def get_ppi_norm_crops(norm_res, ppi_norm_offsets, manual_offsets):
    crops = []
    for i, (res, offs) in enumerate(zip(norm_res, ppi_norm_offsets)):
        try:
            off = manual_offsets[i]
        except IndexError:
            off = (0, 0)
        left_top = (round(offs[0] + off[0]), round(offs[1] + off[1]))
        right_btm = (round(res[0]) + left_top[0], round(res[1]) + left_top[1])
        crops.append(left_top + right_btm)
    return crops


def compute_working_canvas(crop_tuples):
    rightmost = max(crop_tuples, key=itemgetter(2))[2]
    bottommost = max(crop_tuples, key=itemgetter(3))[3]
    return [rightmost, bottommost]


def resize_to_fill(img, res, quality=Image.LANCZOS):
    if not img.mode == "RGB":
        img = img.convert("RGB")
    image_size = img.size
    if image_size == res:
        return img
    image_ratio = image_size[0] / image_size[1]
    target_ratio = res[0] / res[1]
    if image_ratio < target_ratio:
        resize_multiplier = res[0] / image_size[0]
        new_size = (round(resize_multiplier * image_size[0]),
                    round(resize_multiplier * image_size[1]))
        img = img.resize(new_size, resample=quality)
        extra_height = new_size[1] - res[1]
        if extra_height == 0:
            return img
        crop_tuple = (0, round(extra_height / 2), new_size[0],
                      round(extra_height / 2) + res[1])
        return img.crop(crop_tuple)
    resize_multiplier = res[1] / image_size[1]
    new_size = (round(resize_multiplier * image_size[0]),
                round(resize_multiplier * image_size[1]))
    img = img.resize(new_size, resample=quality)
    extra_width = new_size[0] - res[0]
    if extra_width == 0:
        return img
    crop_tuple = (round(extra_width / 2), 0, round(extra_width / 2) + res[0],
                  new_size[1])
    return img.crop(crop_tuple)


def span_advanced(img, resolutions, crops):
    """span_single_image_advanced without perspective or span groups."""
    canvas = tuple(compute_working_canvas(crops))
    work = resize_to_fill(img, canvas)
    out = []
    for crop, res in zip(crops, resolutions):
        piece = work.crop(crop)
        if piece.size != res:
            piece = piece.resize(res, resample=Image.LANCZOS)
        out.append(piece)
    return out

# ---- end of copied code ------------------------------------------------------


def test_image(w, h):
    """Smooth, detailed synthetic image: gradients, soft shapes, a grid."""
    img = Image.new("RGB", (w, h))
    px = img.load()
    for y in range(h):
        for x in range(w):
            px[x, y] = (
                int(255 * x / (w - 1)),
                int(255 * y / (h - 1)),
                int(127 + 120 * math.sin(x / 37.0) * math.cos(y / 29.0)),
            )
    d = ImageDraw.Draw(img)
    for i in range(6):
        cx, cy = (i + 1) * w // 7, h // 2 + int((h / 4) * math.sin(i))
        r = min(w, h) // 8
        d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=(240, 240 - 30 * i, 40 * i))
    for x in range(0, w, 40):
        d.line((x, 0, x, h), fill=(20, 20, 20), width=2)
    for y in range(0, h, 40):
        d.line((0, y, w, y), fill=(20, 20, 20), width=2)
    return img.filter(ImageFilter.GaussianBlur(1.2))


# Martin's setup at 1/8 scale: laptop 16" 2560x1600, Dell and Acer 34" 3440x1440.
DISPLAYS = [
    {"id": "laptop", "resW": 320, "resH": 200, "widthMM": 344.6, "heightMM": 215.4},
    {"id": "dell", "resW": 430, "resH": 180, "widthMM": 797.2, "heightMM": 333.7},
    {"id": "acer", "resW": 430, "resH": 180, "widthMM": 797.4, "heightMM": 333.4},
]
# Superpaper's saved layout for this system, scaled 1/8, with halves to test rounding.
OFFSETS = [(0.0, 209.0), (320.0, 307.625), (323.25, 0.0)]

CASES = [
    {"name": "wide_image", "image": "wide.png", "size": (960, 400), "manual": []},
    {"name": "tall_image", "image": "tall.png", "size": (600, 800), "manual": []},
    {"name": "manual_offsets", "image": "wide.png", "size": (960, 400),
     "manual": [(0, 0), (2.5, -1.5), (0, 0)]},
]


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    resolutions = [(d["resW"], d["resH"]) for d in DISPLAYS]
    ppis = [compute_ppi((d["resW"], d["resH"]), (d["widthMM"], d["heightMM"])) for d in DISPLAYS]
    norm = compute_ppinorm_resolutions(resolutions, ppis)
    made = {}
    cases_out = []
    for case in CASES:
        if case["image"] not in made:
            test_image(*case["size"]).save(os.path.join(OUT_DIR, case["image"]))
            made[case["image"]] = True
        img = Image.open(os.path.join(OUT_DIR, case["image"]))
        crops = get_ppi_norm_crops(norm, OFFSETS, case["manual"])
        slices = span_advanced(img, resolutions, crops)
        names = []
        for i, s in enumerate(slices):
            n = f"{case['name']}_{i}.png"
            s.save(os.path.join(OUT_DIR, n))
            names.append(n)
        cases_out.append({
            "name": case["name"],
            "image": case["image"],
            "manual": [list(m) for m in case["manual"]],
            "normResolutions": [list(n) for n in norm],
            "crops": [list(c) for c in crops],
            "canvas": compute_working_canvas(crops),
            "slices": names,
        })
    with open(os.path.join("testdata", "golden.json"), "w") as f:
        json.dump({"displays": DISPLAYS, "offsets": [list(o) for o in OFFSETS],
                   "cases": cases_out}, f, indent=2)
    print("wrote", len(cases_out), "cases")


if __name__ == "__main__":
    main()
