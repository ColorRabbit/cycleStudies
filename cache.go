package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type PermissionCache struct {
	Channels  map[string]bool // channelID -> hasAccess
	ExpiresAt time.Time
}

var permCacheMu sync.RWMutex
var permCache = make(map[string]*PermissionCache) // key: userID+guildID

// 从缓存获取用户权限（如果未过期）
func getPermissionFromCache(userID, guildID string) (map[string]bool, bool) {
	permCacheMu.RLock()
	defer permCacheMu.RUnlock()

	key := userID + ":" + guildID
	if cache, exists := permCache[key]; exists && time.Now().Before(cache.ExpiresAt) {
		fmt.Printf("✅ 使用缓存权限 (user=%s)\n", userID)
		return cache.Channels, true
	}
	return nil, false
}

// 保存权限到缓存（2小时过期）
func setPermissionCache(userID, guildID string, channels map[string]bool) {
	permCacheMu.Lock()
	defer permCacheMu.Unlock()

	key := userID + ":" + guildID
	permCache[key] = &PermissionCache{
		Channels:  channels,
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	fmt.Printf("💾 缓存权限 (user=%s, ttl=5min)\n", userID)
}

// 缓存 guild 角色权限（1天过期）
var rolePermsCacheMu sync.RWMutex
var rolePermsCache = make(map[string]map[string]uint64)
var rolePermsCacheTime = make(map[string]time.Time)

func getGuildRolesPermsWithCache(token, guildID string) (map[string]uint64, error) {
	rolePermsCacheMu.RLock()
	if cache, exists := rolePermsCache[guildID]; exists && time.Now().Before(rolePermsCacheTime[guildID].Add(24*time.Hour)) {
		rolePermsCacheMu.RUnlock()
		fmt.Printf("✅ 使用缓存 guild roles (guild=%s)\n", guildID)
		return cache, nil
	}
	rolePermsCacheMu.RUnlock()

	// 调用 API 获取
	rolePerms, err := getGuildRolesPerms(token, guildID)
	if err == nil {
		rolePermsCacheMu.Lock()
		rolePermsCache[guildID] = rolePerms
		rolePermsCacheTime[guildID] = time.Now()
		rolePermsCacheMu.Unlock()
	}
	return rolePerms, err
}

// ==========================================
// 消息持久化缓存
// ==========================================

// 确保 cache 目录存在
func ensureCacheDir() error {
	if _, err := os.Stat(CacheDir); os.IsNotExist(err) {
		if err := os.MkdirAll(CacheDir, 0755); err != nil {
			return fmt.Errorf("创建缓存目录失败: %w", err)
		}
		fmt.Printf("📁 创建缓存目录: %s\n", CacheDir)
	}
	return nil
}

// 将消息保存到本地文件
func saveMsgToCache(filename string, msgs []DiscordMessage) error {
	if err := ensureCacheDir(); err != nil {
		return err
	}

	path := filepath.Join(CacheDir, filename)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建缓存文件失败: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(msgs); err != nil {
		return fmt.Errorf("写入缓存失败: %w", err)
	}

	fmt.Printf("💾 已保存 %d 条消息到 %s\n", len(msgs), path)
	return nil
}

// 从本地缓存文件加载消息
func loadMsgCache() {
	var count int
	for _, cfg := range dynamicPostList {
		// 优先从 cache 目录加载
		if msgs, err := loadMsgFromCache(cfg.FileName); err == nil {
			sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
			memoryStore[cfg.FileName] = msgs
			count++
			fmt.Printf("📦 从缓存加载: %s (%d 条)\n", cfg.FileName, len(msgs))
			continue
		}
	}
}
func loadMsgFromCache(filename string) ([]DiscordMessage, error) {
	path := filepath.Join(CacheDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var msgs []DiscordMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("解析缓存文件失败: %w", err)
	}

	return msgs, nil
}
