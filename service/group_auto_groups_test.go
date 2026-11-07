package service

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func configureRequestAutoGroupsTest(t *testing.T) {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","svip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","svip":"SVIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"svip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func newRequestAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestGetRequestAutoGroupsInheritedListIsNotLimited(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default", "svip"}, groups)
}

func TestGetRequestAutoGroupsFiltersBeforeApplyingCurrentLimit(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"revoked", "vip", "default", "svip"})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default"}, groups)
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	assert.Equal(t, []string{"vip"}, GetRequestAutoGroups(ctx, "default"))
}

func TestGetRequestAutoGroupsDoesNotFallBackAfterPermissionChange(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Empty(t, groups)
}

// configureAffordableAutoGroupsTest wires a free group next to two paid ones,
// mirroring the production layout where `default` is ratio 0 and the paid
// groups are ratio 1 and above.
func configureAffordableAutoGroupsTest(t *testing.T) *gorm.DB {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalGroupGroupRatios := ratio_setting.GroupGroupRatio2JSONString()
	originalDB := model.DB
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("10"))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","paid","paid-premium"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","paid":"Paid","paid-premium":"Paid Premium"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"paid":1,"paid-premium":2}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"default":0,"paid":0.5}}`))

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.UserSubscription{}))
	model.DB = db

	t.Cleanup(func() {
		model.DB = originalDB
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalGroupGroupRatios))
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

// addActiveSubscription inserts a subscription that expires far in the future.
func addActiveSubscription(t *testing.T, db *gorm.DB, userId int) {
	t.Helper()
	now := common.GetTimestamp()
	require.NoError(t, db.Create(&model.UserSubscription{
		UserId:      userId,
		AmountTotal: 1000,
		StartTime:   now - 60,
		EndTime:     now + 3600,
		Status:      "active",
	}).Error)
}

func TestGetRequestAutoGroupsDropsPaidGroupsForEmptyWallet(t *testing.T) {
	configureAffordableAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, 0)
	common.SetContextKey(ctx, constant.ContextKeyUserId, 8)

	groups := GetRequestAutoGroups(ctx, "default")

	// `default` costs nothing for this user (group-group ratio 0) and survives;
	// `paid` is 0.5 and `paid-premium` is 2, so an empty wallet cannot reach them.
	assert.Equal(t, []string{"default"}, groups)
}

func TestGetRequestAutoGroupsKeepsPaidGroupsForFundedWallet(t *testing.T) {
	configureAffordableAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, 1)
	common.SetContextKey(ctx, constant.ContextKeyUserId, 2)

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"default", "paid", "paid-premium"}, groups)
}

func TestGetRequestAutoGroupsKeepsPaidGroupsForSubscriptionHolder(t *testing.T) {
	db := configureAffordableAutoGroupsTest(t)
	addActiveSubscription(t, db, 4)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, 0)
	common.SetContextKey(ctx, constant.ContextKeyUserId, 4)

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"default", "paid", "paid-premium"}, groups)
}

func TestGetRequestAutoGroupsKeepsPaidGroupsWhenWalletStateUnknown(t *testing.T) {
	configureAffordableAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	// No quota in the context: the caller did not run the user auth path.
	common.SetContextKey(ctx, constant.ContextKeyUserId, 6)

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"default", "paid", "paid-premium"}, groups)
}

func TestGetRequestAutoGroupsFailsOpenWhenSubscriptionLookupFails(t *testing.T) {
	configureAffordableAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, 0)
	common.SetContextKey(ctx, constant.ContextKeyUserId, 9)
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"default", "paid", "paid-premium"}, groups)
}
