package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func GetUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	if userGroup != "" {
		specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
		if b {
			// 处理特殊可用分组
			for specialGroup, desc := range specialSettings {
				if after, ok := strings.CutPrefix(specialGroup, "-:"); ok {
					// 移除分组
					groupToRemove := after
					delete(groupsCopy, groupToRemove)
				} else if after, ok := strings.CutPrefix(specialGroup, "+:"); ok {
					// 添加分组
					groupToAdd := after
					groupsCopy[groupToAdd] = desc
				} else {
					// 直接添加分组
					groupsCopy[specialGroup] = desc
				}
			}
		}
		// 如果userGroup不在UserUsableGroups中，返回UserUsableGroups + userGroup。
		// 描述是英文源文本，由 web 控制台翻译；管理员配置的描述原样显示。
		if _, ok := groupsCopy[userGroup]; !ok {
			groupsCopy[userGroup] = "User group"
		}
	}
	return groupsCopy
}

func GroupInUserUsableGroups(userGroup, groupName string) bool {
	_, ok := GetUserUsableGroups(userGroup)[groupName]
	return ok
}

func IsUserSelectableGroup(userGroup, groupName string) bool {
	if groupName == "" || groupName == "auto" {
		return false
	}
	return GroupInUserUsableGroups(userGroup, groupName) && ratio_setting.ContainsGroupRatio(groupName)
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(userGroup string) []string {
	autoGroups := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range setting.GetAutoGroups() {
		if !IsUserSelectableGroup(userGroup, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

// FilterUserTokenAutoGroups applies current permissions before the current
// per-token limit. It intentionally does not fall back to the global Auto list.
func FilterUserTokenAutoGroups(userGroup string, groups []string) []string {
	maxCount := setting.GetMaxTokenAutoGroups()
	filtered := make([]string, 0, min(len(groups), maxCount))
	seen := make(map[string]struct{})
	for _, group := range groups {
		if !IsUserSelectableGroup(userGroup, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		filtered = append(filtered, group)
		if len(filtered) == maxCount {
			break
		}
	}
	return filtered
}

// GetRequestAutoGroups resolves the ordered Auto groups for the current token.
// The absence of the context value means that the token inherits the complete
// global Auto list; a present (even empty) value is an explicit token snapshot.
//
// The resolved list is then narrowed to the groups the caller can actually pay
// for. Auto selects a group by channel availability alone, so without this
// filter a zero-balance user walks past the free groups, lands in a paid one,
// and the request dies in pre-consume with "Insufficient user quota" instead of
// reaching the free group that would have served it.
func GetRequestAutoGroups(c *gin.Context, userGroup string) []string {
	value, ok := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
	if !ok {
		return filterUnaffordableAutoGroups(c, userGroup, GetUserAutoGroup(userGroup))
	}
	groups, ok := value.([]string)
	if !ok {
		return []string{}
	}
	return filterUnaffordableAutoGroups(c, userGroup, FilterUserTokenAutoGroups(userGroup, groups))
}

// ginKeyAffordableAutoGroups caches the affordability verdict for one request.
// Auto group resolution runs on every channel attempt, so the subscription
// lookup must not repeat per retry.
const ginKeyAffordableAutoGroups = "affordable_auto_groups"

// walletEmpty reports whether pre-consume would be refused outright. Wallet
// funding rejects at userQuota <= 0 before it ever compares the estimated cost,
// so an empty wallet cannot pay for any positive group ratio no matter how
// small the request is.
//
// A missing context value means the caller never ran the user auth path, so the
// wallet state is unknown rather than empty; treating it as empty would strip
// paid groups from requests that have no wallet at all (channel tests, pinned
// admin calls). Only a present, non-positive quota counts as empty.
func walletEmpty(c *gin.Context) bool {
	quota, known := common.GetContextKeyType[int](c, constant.ContextKeyUserQuota)
	return known && quota <= 0
}

// fundedElsewhere reports whether something other than the wallet can pay:
// either the wallet is not empty, or the user holds an active subscription.
// The verdict is cached for the request because auto group resolution runs once
// per channel attempt.
func fundedElsewhere(c *gin.Context) bool {
	if cached, ok := c.Get(ginKeyAffordableAutoGroups); ok {
		if funded, isBool := cached.(bool); isBool {
			return funded
		}
	}
	funded := false
	if !walletEmpty(c) {
		funded = true // the wallet can pay; no need to consult subscriptions
	} else if userId := common.GetContextKeyInt(c, constant.ContextKeyUserId); userId > 0 {
		hasSub, err := model.HasActiveUserSubscription(userId)
		if err != nil {
			// Fail open: a lookup error must not silently shrink a paying
			// user's routing options.
			common.SysLog(common.LogText("failed to check active subscription for user %d: %s", userId, err.Error()))
			funded = true
		} else {
			funded = hasSub
		}
	}
	c.Set(ginKeyAffordableAutoGroups, funded)
	return funded
}

// filterUnaffordableAutoGroups drops the groups whose ratio is above zero when
// wallet funding is the only source. A subscription can still pay, so users
// holding one keep the full list. Free groups, and the order of what remains,
// are left untouched.
func filterUnaffordableAutoGroups(c *gin.Context, userGroup string, groups []string) []string {
	if c == nil || len(groups) == 0 || fundedElsewhere(c) {
		return groups
	}

	kept := make([]string, 0, len(groups))
	for _, group := range groups {
		ratio := GetUserGroupRatio(userGroup, group)
		if ratio > 0 {
			logger.LogDebug(c, "auto group %s skipped: empty wallet cannot pay ratio %v", group, ratio)
			continue
		}
		kept = append(kept, group)
	}
	return kept
}

// GetGroupsEnabledModels 按 groups 顺序获取各分组启用的模型并去重
func GetGroupsEnabledModels(groups []string) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, group := range groups {
		for _, modelName := range model.GetGroupEnabledModels(group) {
			if _, ok := seen[modelName]; !ok {
				seen[modelName] = struct{}{}
				models = append(models, modelName)
			}
		}
	}
	return models
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if ok {
		return ratio
	}
	return ratio_setting.GetGroupRatio(group)
}
