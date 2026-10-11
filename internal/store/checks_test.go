package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/engine"
)

func newCheck(t *testing.T, db *DB, projectID int64, name, slug, tags string) *engine.Check {
	t.Helper()
	c, err := db.CreateCheck(context.Background(), &NewCheck{
		ProjectID: projectID,
		Name:      name,
		Slug:      slug,
		Tags:      tags,
		Kind:      engine.KindSimple,
		Timeout:   engine.DefaultTimeout,
		Grace:     engine.DefaultGrace,
		TZ:        "UTC",
	})
	if err != nil {
		t.Fatalf("create %q: %v", name, err)
	}
	return c
}

func TestCheckLookups(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	c := newCheck(t, db, p.ID, "夜间任务", "nightly", "prod")

	byCode, err := db.CheckByCode(ctx, c.Code)
	if err != nil {
		t.Fatal(err)
	}
	if byCode.Name != "夜间任务" || byCode.Slug != "nightly" || byCode.Tags != "prod" {
		t.Errorf("CheckByCode = %+v", byCode)
	}
	if byCode.Created.IsZero() {
		t.Error("created should round-trip")
	}

	if _, err := db.CheckByCode(ctx, "no-such-code"); err != ErrNotFound {
		t.Errorf("missing code = %v, want ErrNotFound", err)
	}

	byBadge, err := db.CheckByBadgeKey(ctx, c.BadgeKey)
	if err != nil {
		t.Fatalf("CheckByBadgeKey: %v", err)
	}
	if byBadge.ID != c.ID {
		t.Errorf("badge key resolved to %d, want %d", byBadge.ID, c.ID)
	}
	if _, err := db.CheckByBadgeKey(ctx, "wrong"); err != ErrNotFound {
		t.Errorf("missing badge key = %v, want ErrNotFound", err)
	}

	bySlug, err := db.CheckBySlug(ctx, p.ID, "nightly")
	if err != nil || bySlug.ID != c.ID {
		t.Errorf("CheckBySlug = %+v, %v", bySlug, err)
	}
	// 别的项目的同名 slug 不能串味。
	if _, err := db.CheckBySlug(ctx, c.ID+999, "nightly"); err != ErrNotFound {
		t.Errorf("other project slug = %v, want ErrNotFound", err)
	}

	strict, err := db.CheckBySlugStrict(ctx, p.ID, "nightly")
	if err != nil || strict.ID != c.ID {
		t.Errorf("CheckBySlugStrict = %+v, %v", strict, err)
	}
	if _, err := db.CheckBySlugStrict(ctx, p.ID, "ghost"); err != ErrNotFound {
		t.Errorf("missing slug = %v, want ErrNotFound", err)
	}

	if _, err := db.CheckByID(ctx, c.ID+1234); err != ErrNotFound {
		t.Errorf("missing id = %v, want ErrNotFound", err)
	}
}

func TestCreateCheckRejectsDuplicateSlug(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)

	first := newCheck(t, db, p.ID, "甲", "dup", "")
	// slug 上有 partial 唯一索引，空 slug 不受约束。
	second := newCheck(t, db, p.ID, "乙", "", "")
	if first.ID == second.ID {
		t.Fatal("blank slugs must not collide")
	}

	_, err := db.CreateCheck(context.Background(), &NewCheck{
		ProjectID: p.ID, Name: "丙", Slug: "dup", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	})
	if err == nil {
		t.Fatal("duplicate slug should violate the unique index")
	}
}

func TestCreateCheckMaxChecksLimit(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	db.MaxChecks = 2

	for i := 0; i < 2; i++ {
		newCheck(t, db, p.ID, "任务", "", "")
	}
	if _, err := db.CreateCheck(context.Background(), &NewCheck{
		ProjectID: p.ID, Name: "超限", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	}); err != ErrTooManyChecks {
		t.Errorf("err = %v, want ErrTooManyChecks", err)
	}

	// 上限为 0 表示不限制。
	db.MaxChecks = 0
	if _, err := db.CreateCheck(context.Background(), &NewCheck{
		ProjectID: p.ID, Name: "不限", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	}); err != nil {
		t.Errorf("MaxChecks=0 should mean unlimited: %v", err)
	}

	n, err := db.CountChecks(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("CountChecks = %d, want 3", n)
	}
}

func TestListChecksFiltersAndOrders(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	// 插入顺序即 created 升序：alpha, beta, zulu。
	newCheck(t, db, p.ID, "beta", "beta", "prod db")
	newCheck(t, db, p.ID, "alpha", "alpha", "db")
	newCheck(t, db, p.ID, "zulu", "zulu", "prod")

	names := func(list []*engine.Check) []string {
		out := make([]string, 0, len(list))
		for _, c := range list {
			out = append(out, c.Name)
		}
		return out
	}
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	all, err := db.ListChecks(ctx, p.ID, CheckFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// 默认按 created 倒序，最新建的排前面。
	if !eq(names(all), []string{"zulu", "alpha", "beta"}) {
		t.Errorf("default order = %v", names(all))
	}

	byName, err := db.ListChecks(ctx, p.ID, CheckFilter{OrderBy: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(byName), []string{"alpha", "beta", "zulu"}) {
		t.Errorf("order by name = %v", names(byName))
	}

	byNameDesc, err := db.ListChecks(ctx, p.ID, CheckFilter{OrderBy: "-name"})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(byNameDesc), []string{"zulu", "beta", "alpha"}) {
		t.Errorf("order by -name = %v", names(byNameDesc))
	}

	byCreated, err := db.ListChecks(ctx, p.ID, CheckFilter{OrderBy: "created"})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(byCreated), []string{"beta", "alpha", "zulu"}) {
		t.Errorf("order by created = %v", names(byCreated))
	}

	// 没有 ping 过的检查项 last_ping 为 NULL，SQLite 把 NULL 当最小值，
	// 所以 -last_ping 只剩插入顺序可辨；这里只验证不报错、条数一致。
	byPing, err := db.ListChecks(ctx, p.ID, CheckFilter{OrderBy: "-last_ping"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPing) != 3 {
		t.Errorf("order by -last_ping returned %d checks", len(byPing))
	}

	tags, err := db.ListChecks(ctx, p.ID, CheckFilter{Tags: []string{"db"}})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(tags), []string{"alpha", "beta"}) {
		t.Errorf("tag db = %v, want the two checks carrying it", names(tags))
	}

	// 整词匹配：「pr」不该命中「prod」。
	partial, err := db.ListChecks(ctx, p.ID, CheckFilter{Tags: []string{"pr"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 0 {
		t.Errorf("prefix tag matched %v, want nothing", names(partial))
	}

	slugs, err := db.ListChecks(ctx, p.ID, CheckFilter{Slugs: []string{"beta", "zulu"}})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(slugs), []string{"zulu", "beta"}) {
		t.Errorf("slug filter = %v", names(slugs))
	}

	// 另一个项目的检查项不该出现。
	projectB := secondProject(t, db, "另一个项目")
	other, err := db.CreateCheck(ctx, &NewCheck{
		ProjectID: projectB, Name: "别的项目", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := db.ListChecks(ctx, other.ProjectID, CheckFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != other.ID {
		t.Errorf("project scoping returned %+v", list)
	}
}

// secondProject inserts an extra project row directly: Seed only ever maintains
// the single default one, and project_id has a foreign key.
func secondProject(t *testing.T, db *DB, name string) int64 {
	t.Helper()
	res, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO projects(code, name, badge_key, ping_key, show_slugs, created)
		 VALUES(?,?,?,?,0,?)`,
		uuid.NewString(), name, uuid.NewString(), uuid.NewString(), toDB(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDistinctTags(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	newCheck(t, db, p.ID, "甲", "", "prod db")
	newCheck(t, db, p.ID, "乙", "", "prod")
	newCheck(t, db, p.ID, "丙", "", "")

	tags, err := db.DistinctTags(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		seen[tag] = true
	}
	if len(tags) != 2 || !seen["prod"] || !seen["db"] {
		t.Errorf("DistinctTags = %v, want prod and db", tags)
	}

	// 空项目返回空列表而不是 nil 错误。
	empty, err := db.DistinctTags(ctx, p.ID+99)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("other project tags = %v", empty)
	}
}

func TestCheckChannelAssignment(t *testing.T) {
	db := testDB(t)
	project := testProject(t, db)
	ctx := context.Background()

	var ids []int64
	for _, name := range []string{"甲", "乙", "丙"} {
		ch, err := db.CreateChannel(ctx, &NewChannel{
			ProjectID: project.ID, Name: name, URLs: []string{"json://localhost"},
			Conf: ChannelConf{Up: true, Down: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ch.ID)
	}

	all, err := db.AllChannelIDs(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("AllChannelIDs = %v, want 3", all)
	}

	c := newCheck(t, db, project.ID, "带渠道", "", "")
	if err := db.SetCheckChannels(ctx, c.ID, ids[:2]); err != nil {
		t.Fatal(err)
	}
	assigned, err := db.CheckChannels(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 2 || assigned[0] != ids[0] || assigned[1] != ids[1] {
		t.Errorf("assigned = %v, want the first two channels", assigned)
	}

	// 替换语义：重新赋值会把旧的连接清掉。
	if err := db.SetCheckChannels(ctx, c.ID, []int64{ids[2]}); err != nil {
		t.Fatal(err)
	}
	assigned, err = db.CheckChannels(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 1 || assigned[0] != ids[2] {
		t.Errorf("after replace = %v, want only %d", assigned, ids[2])
	}

	// 未知渠道会被外键挡下来。
	if err := db.SetCheckChannels(ctx, c.ID, []int64{9999}); err == nil {
		t.Error("assigning an unknown channel should fail")
	}

	none, err := db.CheckChannels(ctx, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("unknown check channels = %v", none)
	}
}

func TestUpdateCheckRoundTrip(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	c := newCheck(t, db, p.ID, "原名", "orig", "a")
	c.Name = "改名"
	c.Slug = "renamed"
	c.Tags = "prod"
	c.Description = "描述"
	c.Kind = engine.KindCron
	c.Schedule = "0 3 * * *"
	c.TZ = "Asia/Shanghai"
	c.Methods = "POST"
	c.ManualResume = true
	c.FilterHTTPBody = true
	c.FilterDefaultFail = true
	c.StartKW, c.SuccessKW, c.FailureKW = "开始", "成功", "失败"
	c.FailTolerance = 2
	c.Status = engine.StatusUp
	alert := time.Now().UTC().Truncate(time.Microsecond)
	c.AlertAfter = &alert
	rid := "rid-1"
	c.LastStartRid = &rid

	if err := db.UpdateCheck(ctx, c); err != nil {
		t.Fatal(err)
	}

	got, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "改名" || got.Slug != "renamed" || got.Description != "描述" {
		t.Errorf("identity fields not persisted: %+v", got)
	}
	if got.Kind != engine.KindCron || got.Schedule != "0 3 * * *" || got.TZ != "Asia/Shanghai" {
		t.Errorf("schedule fields: %+v", got)
	}
	if got.Methods != "POST" || !got.ManualResume || !got.FilterHTTPBody || !got.FilterDefaultFail {
		t.Errorf("boolean/flags round-trip: %+v", got)
	}
	if got.StartKW != "开始" || got.SuccessKW != "成功" || got.FailureKW != "失败" {
		t.Errorf("keywords: %+v", got)
	}
	if got.FailTolerance != 2 {
		t.Errorf("fail tolerance = %d", got.FailTolerance)
	}
	if got.Status != engine.StatusUp {
		t.Errorf("status = %q", got.Status)
	}
	if got.AlertAfter == nil || !got.AlertAfter.Equal(alert) {
		t.Errorf("alert_after = %v, want %v", got.AlertAfter, alert)
	}
	if got.LastStartRid == nil || *got.LastStartRid != "rid-1" {
		t.Errorf("last_start_rid = %v", got.LastStartRid)
	}
	if got.LastPing != nil || got.LastStart != nil || got.LastDuration != nil {
		t.Errorf("null timestamps should stay null: %+v", got)
	}
}
