"""生成发型封面占位图。

正式素材（AI 生成或实拍）到位后直接覆盖同名文件即可，
本脚本只是让流程在素材空缺时也能完整跑通。
"""
import json
from pathlib import Path

from PIL import Image, ImageDraw

OUT = Path(__file__).resolve().parents[1] / "data" / "hairstyles" / "covers"

# 每款一个主色，仅用于视觉上区分，不代表实际配色。
PALETTE = {
    "m": (110, 96, 130),
    "f": (130, 96, 118),
}


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    lib = json.loads((OUT.parent / "library.json").read_text(encoding="utf-8"))

    for style in lib:
        path = OUT / f"{style['id']}.jpg"
        if path.exists():
            continue

        base = PALETTE.get(style["id"][0], (110, 96, 130))
        img = Image.new("RGB", (480, 600), base)
        draw = ImageDraw.Draw(img)

        # 简单的头肩剪影，让占位图不至于完全空白
        draw.ellipse((150, 120, 330, 340),
                     fill=(base[0] + 26, base[1] + 26, base[2] + 26))
        draw.ellipse((110, 400, 370, 720),
                     fill=(base[0] + 14, base[1] + 14, base[2] + 14))

        img.save(path, quality=82)
        print("wrote", path.name)


if __name__ == "__main__":
    main()
