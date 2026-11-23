# uploads/rec —— 发型封面图目录

把发型预览图直接放进这个目录即可，**不需要重新编译或重启**，刷新列表就会生效。

## 命名规则

文件名必须等于发型库里那一款的 `style_id`：

    {style_id}.jpg

例如：

    m_crop_textured.jpg      → 清爽层次碎盖
    f_curtain_bangs.jpg      → 八字刘海中长发

## 支持的格式

.jpg / .jpeg / .png / .webp，按这个顺序查找，先找到先用。

## 优先级

    uploads/rec  >  data/hairstyles/covers

在这里放了同名文件，就会覆盖 data/ 里的占位图。
删掉这里的文件，会自动回落到 data/hairstyles/covers 的占位图，列表不会出现空白。

## 建议尺寸

正方形或 4:5，最短边不小于 400px。
列表里按 aspectFill 裁切显示，非正方形也不会变形。

## 全部 style_id 清单

男款：
    m_crop_textured   清爽层次碎盖
    m_side_part_short 经典三七侧分
    m_buzz_clean      干净短寸
    m_fringe_soft     自然前额碎刘海
    m_mid_part_wave   中分微卷中短发
    m_undercut_volume 高顶两侧推短
    m_curly_short     松散小卷短发
    m_slick_back      后梳整齐背头
    m_bowl_soft       轻柔圆盖短发
    m_long_layer_tie  可扎长层次

女款：
    f_shoulder_layer   锁骨层次长发
    f_air_bangs_short  空气刘海短发
    f_mid_wave_long    中长微卷披发
    f_side_part_bob    侧分波波头
    f_ponytail_high    高马尾扎发
    f_curtain_bangs    八字刘海中长发
    f_short_pixie      轻盈短发
    f_braid_daily      日常侧编发
    f_layer_face_frame 面部轮廓层次发
    f_soft_waves_light 浅调自然卷
