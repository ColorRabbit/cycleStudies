package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// ==========================================
// 控制器层 (Handlers)
// ==========================================

func main() {
	initService()

	// 路由注册
	http.HandleFunc("/login", handleLogin)                     // 登录页 & 提交
	http.HandleFunc("/logout", handleLogout)                   // 登出
	http.HandleFunc("/refresh", authMiddleware(handleRefresh)) // 刷新 (需登录)
	http.HandleFunc("/", authMiddleware(handleIndex))          // 主页 (需登录)

	link := "http://localhost:" + Port
	fmt.Println("-------------------------------------------")
	fmt.Println("✅ 聊天存档查看器已启动")
	fmt.Printf("👉 请访问: %s\n", link)
	fmt.Println("-------------------------------------------")

	openBrowser(link)
	log.Fatal(http.ListenAndServe(":"+Port, nil))
}

// 中间件：验证登录状态
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// 简单解码 Session (实际生产环境应加密)
		jsonBytes, err := base64.StdEncoding.DecodeString(cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		var session UserSession
		if err := json.Unmarshal(jsonBytes, &session); err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if len(dynamicPostList) == 0 {
			currentUser := getCurrentUser(r)
			fmt.Printf("🔄 用户 [%s] 鉴权成功，开始从接口获取频道配置...\n", currentUser.Username)
			configs, fetchConfigErr := fetchPostConfigurations() // 假设接口需要 token
			if fetchConfigErr != nil {
				log.Printf("❌ 获取频道配置失败: %v\n", fetchConfigErr)
				renderLogin(w, "获取频道配置失败")
				return
			}

			// 成功获取后，更新全局的 dynamicPostList
			dynamicPostList = configs
			loadMsgCache()
			fmt.Printf("✅ 成功获取 %d 个频道配置，开始预加载消息...\n", len(configs))
		}

		next(w, r)
	}
}

// 获取当前用户 helper
func getCurrentUser(r *http.Request) *UserSession {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	bytes, _ := base64.StdEncoding.DecodeString(cookie.Value)
	var user UserSession
	json.Unmarshal(bytes, &user)
	return &user
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		token := r.FormValue("token")
		user, err := verifyToken(token)
		if err != nil {
			renderLogin(w, err.Error())
			return
		}

		// 创建 Session
		sessionBytes, _ := json.Marshal(user)
		encoded := base64.StdEncoding.EncodeToString(sessionBytes)

		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    encoded,
			Path:     "/",
			HttpOnly: true,
			MaxAge:   3600 * 24 * 30, // 30天
		})

		// --- 用户请求的新增逻辑：在登录时预加载所有频道的历史消息 ---
		fmt.Printf("🔄 用户 [%s] 登录成功，开始从接口获取频道配置...\n", user.Username)
		configs, fetchConfigErr := fetchPostConfigurations() // 假设接口需要 token
		if fetchConfigErr != nil {
			log.Printf("❌ 获取频道配置失败: %v\n", fetchConfigErr)
			renderLogin(w, "获取频道配置失败")
			return
		}

		// 成功获取后，更新全局的 dynamicPostList
		dynamicPostList = configs
		fmt.Printf("✅ 成功获取 %d 个频道配置，开始预加载消息...\n", len(configs))
		//for _, cfg := range ChannelList {
		//	// 调用 fetchNewMessages 获取特定 PostID 的所有历史消息。
		//	// 假设 fetchNewMessages 的第二个参数是 PostID，第三个参数 sinceID 为空字符串表示获取所有历史消息。
		//	fetchedMsgs, fetchErr := fetchNewMessages(user.Token, cfg.PostID, "")
		//	if fetchErr != nil {
		//		log.Printf("❌ 预加载频道 [%s] 消息失败 (PostID: %s): %v\n", cfg.FileName, cfg.PostID, fetchErr)
		//		// 即使某个频道预加载失败，也继续处理其他频道，避免阻塞登录流程
		//		continue
		//	}
		//
		//	storeMu.Lock()                          // 在修改 memoryStore 之前锁定互斥锁，确保并发安全
		//	memoryStore[cfg.FileName] = fetchedMsgs // 使用 FileName 作为键，将获取到的消息覆盖或存储
		//	storeMu.Unlock()                        // 修改完成后解锁
		//	fmt.Printf("✅ 预加载频道 [%s] 消息成功，共 %d 条.\n", cfg.FileName, len(fetchedMsgs))
		//}
		//fmt.Println("✅ 所有频道消息预加载完成。")
		// --- 新增逻辑结束 ---

		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	renderLogin(w, "")
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   CookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	currentUser := getCurrentUser(r)
	if currentUser == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// 获取用户可访问的所有频道
	fmt.Printf("🔐 正在获取用户 [%s] 的频道权限...\n", currentUser.Username)
	accessibleChannels, err := getUserAllAccessibleChannels(currentUser.Token, currentUser.UserID)
	if err != nil {
		fmt.Printf("⚠️  权限获取失败: %v\n", err)
		accessibleChannels = make(map[string]bool)
	}

	// 检查用户是否有权访问此频道
	if !accessibleChannels[ChannelID] {
		fmt.Printf("⛔ 用户 [%s] 无权访问频道\n", currentUser.Username)
		renderLogin(w, "无权访问频道")
		return
	}

	activeFile := r.URL.Query().Get("f")
	if activeFile == "" && len(dynamicPostList) > 0 {
		activeFile = dynamicPostList[0].FileName
	}

	var navItems []NavItem
	dynamicPostListMu.RLock()
	for _, cfg := range dynamicPostList {
		msgs, exists := memoryStore[cfg.FileName]
		count := "0"
		if exists {
			count = fmt.Sprintf("%d", len(msgs))
		}
		navItems = append(navItems, NavItem{
			MonthStr: cfg.MonthStr,
			Title:    cfg.Title,
			SubTitle: cfg.SubTitle,
			FileName: cfg.FileName,
			Count:    count + "条",
			IsActive: (cfg.FileName == activeFile),
		})
	}
	dynamicPostListMu.RUnlock()

	var nodes []*ViewNode
	if msgs, ok := memoryStore[activeFile]; ok {
		nodes = buildViewNodes(msgs, currentUser.UserID)
	}

	renderHome(w, PageData{
		NavItems:    navItems,
		Messages:    nodes,
		ActiveFile:  activeFile,
		ProxyInfo:   ProxyURL,
		CurrentUser: currentUser,
	})
}

func handleRefresh(w http.ResponseWriter, r *http.Request) {
	currentUser := getCurrentUser(r)
	if currentUser == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	targetFile := r.URL.Query().Get("f")

	if allowed, waitTime := checkRateLimit(); !allowed {
		renderLimitError(w, waitTime)
		return
	}

	var targetPostID string
	dynamicPostListMu.RLock() // 使用读锁保护
	for _, cfg := range dynamicPostList {
		if cfg.FileName == targetFile {
			targetPostID = cfg.PostID
			break
		}
	}
	dynamicPostListMu.RUnlock()

	if targetPostID == "" {
		log.Printf("错误: 无法为文件 [%s] 找到对应的 PostID\n", targetFile)
		http.Error(w, "Invalid file specified", http.StatusBadRequest)
		return
	}

	var existingMsgs []DiscordMessage
	var hasExistingMsgs bool
	storeMu.Lock()
	existingMsgs, hasExistingMsgs = memoryStore[targetFile]
	storeMu.Unlock()

	sinceID := ""
	if hasExistingMsgs && len(existingMsgs) > 0 {
		// 如果有现有消息，则获取比最新消息 ID 更新的消息
		// 假设 existingMsgs 是按最新到最旧排序的，那么 existingMsgs[0].ID 就是最新的消息 ID。
		sinceID = existingMsgs[0].ID
		fmt.Printf("🔄 用户 [%s] 正在抓取 [%s] 的新消息 (PostID: %s, 从 %s 之后)...\n", currentUser.Username, targetFile, targetPostID, sinceID)
	} else {
		// 如果没有现有消息，或者 targetFile 不在 memoryStore 中，则从头开始抓取所有消息。
		fmt.Printf("🔄 用户 [%s] 正在抓取 [%s] 的所有消息 (PostID: %s, 从头开始)...\n", currentUser.Username, targetFile, targetPostID)
	}

	newlyFetchedMsgs, err := fetchNewMessages(currentUser.Token, targetPostID, sinceID)

	if err == nil {
		storeMu.Lock() // 使用写锁写入 memoryStore
		if sinceID != "" {
			// 如果是增量抓取，将新获取的消息添加到现有消息列表的前面
			// 假设 newlyFetchedMsgs 也是按最新到最旧排序的。
			memoryStore[targetFile] = append(newlyFetchedMsgs, existingMsgs...)
		} else {
			// 如果是完整抓取，直接替换现有消息列表
			memoryStore[targetFile] = newlyFetchedMsgs
		}
		currentTotalMsgs := len(memoryStore[targetFile])

		// 持久化到本地缓存
		if err := saveMsgToCache(targetFile, memoryStore[targetFile]); err != nil {
			fmt.Printf("⚠️  保存缓存失败: %v\n", err)
		}

		storeMu.Unlock() // 释放写锁
		fmt.Printf("✅ 同步 [%s] 成功，当前共 %d 条\n", targetFile, currentTotalMsgs)
	} else {
		fmt.Printf("❌ 同步 [%s] 失败: %v\n", targetFile, err)
	}

	http.Redirect(w, r, "/?f="+targetFile, http.StatusSeeOther)
}
