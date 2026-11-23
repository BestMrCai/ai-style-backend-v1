package hairstyle

import (
	"path/filepath"
	"testing"
)

func loadTestLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Load(filepath.Join("..", "..", "data", "hairstyles"))
	if err != nil {
		t.Fatalf("load library: %v", err)
	}
	return lib
}

// 男性用户不应拿到女性专属发型，反之亦然。
func TestRecommendFiltersByGender(t *testing.T) {
	lib := loadTestLibrary(t)

	male := lib.Recommend(Candidate{Gender: "male", FaceShape: "圆脸"}, 20)
	if len(male) == 0 {
		t.Fatal("male should have recommendations")
	}
	for _, item := range male {
		if item.Gender != "male" {
			t.Errorf("male got female style %s", item.ID)
		}
	}

	female := lib.Recommend(Candidate{Gender: "female", FaceShape: "圆脸"}, 20)
	for _, item := range female {
		if item.Gender != "female" {
			t.Errorf("female got male style %s", item.ID)
		}
	}
}

// u18 本期关闭染发与烫发推荐，但成年档照常可选。
func TestRecommendAgePolicy(t *testing.T) {
	lib := loadTestLibrary(t)

	for _, item := range lib.Recommend(Candidate{Gender: "female", AgeGroup: "u18"}, 50) {
		for _, r := range item.Requires {
			if r == "color" || r == "perm" {
				t.Errorf("u18 got style requiring %s: %s", r, item.ID)
			}
		}
	}

	adult := lib.Recommend(Candidate{Gender: "female", AgeGroup: "26_35"}, 50)
	hasColor := false
	for _, item := range adult {
		for _, r := range item.Requires {
			if r == "color" {
				hasColor = true
			}
		}
	}
	if !hasColor {
		t.Error("adult should still see color styles")
	}
}

// 同分时顺序必须稳定，避免用户刷新看到列表跳动。
func TestRecommendStableOrder(t *testing.T) {
	lib := loadTestLibrary(t)
	c := Candidate{Gender: "male", FaceShape: "鹅蛋脸"}

	first := lib.Recommend(c, 5)
	for i := 0; i < 20; i++ {
		again := lib.Recommend(c, 5)
		for j := range first {
			if first[j].ID != again[j].ID {
				t.Fatalf("order changed: %s vs %s", first[j].ID, again[j].ID)
			}
		}
	}
}

// 脸型命中必须真的提高分数，否则推荐就是随机的。
func TestFaceShapeAffectsScore(t *testing.T) {
	lib := loadTestLibrary(t)

	hit := lib.Recommend(Candidate{Gender: "male", FaceShape: "圆脸"}, 20)
	miss := lib.Recommend(Candidate{Gender: "male", FaceShape: "长脸"}, 20)

	scoreOf := func(items []Recommendation, id string) float64 {
		for _, it := range items {
			if it.ID == id {
				return it.Score
			}
		}
		return -1
	}

	// m_crop_textured 的 fit_shapes 含圆脸不含长脸。
	if scoreOf(hit, "m_crop_textured") <= scoreOf(miss, "m_crop_textured") {
		t.Error("face shape match should raise score")
	}
}

// 缺性别时不做过滤，但不能 panic；由上层负责要求用户补选。
func TestRecommendWithoutGender(t *testing.T) {
	lib := loadTestLibrary(t)
	items := lib.Recommend(Candidate{}, 5)
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}
}

// 负分机制：命中 avoid_shapes 的款式必须真的降分，
// 否则“不建议”就只是一个没人读的字段。
func TestAvoidShapesPenalizes(t *testing.T) {
	lib := loadTestLibrary(t)

	scoreOf := func(items []Recommendation, id string) float64 {
		for _, it := range items {
			if it.ID == id {
				return it.Score
			}
		}
		return 0
	}

	// m_buzz_clean 适合鹅蛋脸，不建议圆脸与梨形脸。
	ok := lib.Recommend(Candidate{Gender: "male", FaceShape: "鹅蛋脸"}, 20)
	bad := lib.Recommend(Candidate{Gender: "male", FaceShape: "圆脸"}, 20)

	if scoreOf(bad, "m_buzz_clean") >= scoreOf(ok, "m_buzz_clean") {
		t.Errorf("avoid_shapes should lower score, got bad=%v ok=%v",
			scoreOf(bad, "m_buzz_clean"), scoreOf(ok, "m_buzz_clean"))
	}
}

// 发际线高度档位必须参与打分。
func TestHairlineLevelAffectsScore(t *testing.T) {
	lib := loadTestLibrary(t)

	scoreOf := func(items []Recommendation, id string) float64 {
		for _, it := range items {
			if it.ID == id {
				return it.Score
			}
		}
		return -1
	}

	// 露额款：发际线偏高时应该比偏低时分数低。
	high := lib.Recommend(Candidate{Gender: "male", FaceShape: "鹅蛋脸", HairlineLevel: "偏高"}, 20)
	low := lib.Recommend(Candidate{Gender: "male", FaceShape: "鹅蛋脸", HairlineLevel: "偏低"}, 20)

	if scoreOf(high, "m_buzz_clean") >= scoreOf(low, "m_buzz_clean") {
		t.Errorf("exposed style should score lower when hairline is high, got high=%v low=%v",
			scoreOf(high, "m_buzz_clean"), scoreOf(low, "m_buzz_clean"))
	}

	// 刘海款则相反。
	if scoreOf(high, "m_fringe_soft") <= scoreOf(low, "m_fringe_soft") {
		t.Errorf("fringe style should score higher when hairline is high, got high=%v low=%v",
			scoreOf(high, "m_fringe_soft"), scoreOf(low, "m_fringe_soft"))
	}
}

// 发量命中也要真的加分。
func TestDensityAffectsScore(t *testing.T) {
	lib := loadTestLibrary(t)

	scoreOf := func(items []Recommendation, id string) float64 {
		for _, it := range items {
			if it.ID == id {
				return it.Score
			}
		}
		return -1
	}

	// m_curly_short 适合稀疏与适中发量。
	sparse := lib.Recommend(Candidate{Gender: "male", FaceShape: "鹅蛋脸", Density: "稀疏"}, 20)
	none := lib.Recommend(Candidate{Gender: "male", FaceShape: "鹅蛋脸"}, 20)

	if scoreOf(sparse, "m_curly_short") <= scoreOf(none, "m_curly_short") {
		t.Errorf("density match should raise score, got sparse=%v none=%v",
			scoreOf(sparse, "m_curly_short"), scoreOf(none, "m_curly_short"))
	}
}
