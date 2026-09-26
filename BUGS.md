# ffhub-filestore 代码审查 / BUG 清单

审查范围：全部 Go 源码（19 个文件）、SQL 迁移、Dockerfile、Woodpecker CI、配置文件。

基线验证：`go build ./...` 通过，`go vet ./...` 通过 —— **下列问题均不会导致编译失败，都是运行时/逻辑问题。**

---

## 一、严重（功能失效 / 数据错误 / 安全）

### BUG-1【P0】上传不存在的文件返回 500，而非 404　—— ✅ 已修复，已验证

- 位置：`internal/handler/upload/upload.go:56-59`
- 代码：
  ```go
  if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
      errs.InternalError().AppendDetails("无法找到文件", "文件上传失败").Respond(c)
      return
  }
  ```
- 问题：文件不存在属于**客户端错误**，应返回 `404 Not Found`，这里返回 `500 Internal Server Error`。
  同时该分支**不记录任何日志**，属于"未处理"的裸 `InternalError()`，不符合本项目其它分支的
  `CreateAndLogInternalError` 惯例。
- 另有隐患：`os.Stat` 返回的**其它错误**（如 `EACCES` 权限不足、`ENOTDIR` 路径中间是文件）
  未被判断，会继续往下走，最终在 `ComputeSHA512` / `CopyFile` 处炸掉，报错信息误导为
  "SHA512 计算失败"。需要补一个 `if err != nil` 的兜底分支。
- 修复：
  ```go
  if _, err := os.Stat(src); err != nil {
      if errors.Is(err, os.ErrNotExist) {
          errs.NotFoundError().AppendDetails("无法找到文件", "文件上传失败").Respond(c)
          return
      }
      errs.CreateAndLogInternalError(err, "Failed to stat source file").
          AppendDetails("文件读取失败", "文件上传失败").Respond(c)
      return
  }
  ```

### BUG-2【P0】`errs.CreatePGError` 丢失所有数据库错误细节，故障无法排查

- 位置：`internal/errs/error.go:67-78`
- 代码：
  ```go
  func CreatePGError(err error, item, log string) *Error {
      if errors.Is(err, pgx.ErrNoRows) {
          return NotFoundError().AppendDetails(fmt.Sprintf("“%s”不存在", item))
      }
      var pgErr *pgconn.PgError
      if !errors.As(err, &pgErr) || !strings.HasPrefix(pgErr.Code, "23") {
          return CreateAndLogInternalError(err, log).AppendDetails("数据库内部错误")
      }
      return ConflictError().AppendDetails(fmt.Sprintf("“%s”冲突", pgErr.ConstraintName))
  }
  ```
- 问题（三个独立缺陷）：
  1. **`pgx.ErrNoRows` 分支响应 404 却完全不写日志**（未调用 `CreateAndLogInternalError`）。
     "预期内"的 404 与"真正的表结构错误"无法区分，线上排查无迹可循。
  2. **约束冲突响应 409 时同样不写日志**，且 `ConflictError()` 的 `Info.Error` 是常量
     `"conflict"`，`pgErr.ConstraintName` 只进了 `details`。
  3. 最关键的：**该函数从不记录 `pgErr.Code`、`pgErr.Message`、`pgErr.Detail`**。
     参数 `log`（如 `"Failed to create file meta"`）只在"非 PG 错误"分支被使用；
     一旦是真实 PG 错误，`log` 字符串被**彻底丢弃**。生产环境出现 `23505` 之外的
     数据库错误时，日志里没有任何可定位信息（连出错的 SQL 场景名都没有）。
- 修复：在 `CreatePGError` 内对所有分支统一记录结构化日志（`pgErr.Code`/`Message`/`Detail`/`ConstraintName`），
  并把 `log` 作为 `msg` 一并输出。

### BUG-3【P0】下载令牌可被无限次复用（私密文件保护形同虚设）

- 位置：`internal/handler/file/file.go:118-127`
- 代码：
  ```go
  s, err := session.GetDownloadSessionByToken(c.Request.Context(), token)
  ...
  session.DeleteDownloadSessionByToken(c.Request.Context(), token)
  ```
- 问题：删除操作发生在**鉴权之后、真正写出文件之前**，且删除是 `db.R.Del(...)` 的
  **fire-and-forget**（返回值被完全丢弃）。后果：
  1. 若 `os.Open(storage.FilePath(meta.SHA512))`（第 130 行）失败——文件缺失或磁盘 IO 错误——
     请求返回 404，**但令牌已经被删除**。用户随即拿到一个"永久失效且无法恢复"的令牌，
     而文件本身仍然存在。这是典型的"先消费后校验"顺序错误，应改为**写出成功后再删除**，
     或至少在 `os.Open` 成功之后才删除。
  2. 令牌删除失败无任何日志，Redis 抖动时会静默产生**可被重复使用的长期令牌**
     （TTL 内无限次下载），私密性保证被削弱。
  3. `fileInfo`（`/api/v1/info/*slug`）对私密文件**只校验令牌、不消费令牌**
     （`file.go:60-68`），这是有意的探测接口，但配合上面两点，令牌泄露后的影响面被放大。
- 修复：将 `DeleteDownloadSessionByToken` 移到文件确认可读之后；改为返回 `error` 并记录日志；
  考虑用 `GETDEL` 实现原子的一次性消费。

### BUG-4【P0】上传中断会永久性丢失源文件（`MoveFile` 跨设备回退路径）

- 位置：`internal/util/file/utils.go:71-81`
- 代码：
  ```go
  func MoveFile(src, dst string) error {
      if err := os.Rename(src, dst); err != nil {
          if err := CopyFile(src, dst); err != nil {
              return fmt.Errorf("copy to dst: %w", err)
          }
          if err := os.Remove(src); err != nil {
              slog.Warn("Failed to remove temp file", "error", err)
          }
      }
      return nil
  }
  ```
- 问题：`os.Rename` 跨文件系统（`SHARED_DIR` 与 `storage/` 常常是两个不同的挂载卷！
  见 Dockerfile 第 47-48 行的两个 `VOLUME`）会返回 `EXDEV`，走 `CopyFile` 回退。当
  `CopyFile` 因磁盘写满、超时等原因失败时，函数**返回错误但源文件保持原样**——这看起来是对的，
  但调用方 `upload.go:99-103` 拿到错误后直接返回 500，**此时 blob 可能只被复制了一半**，
  而 `CopyFile` 内部用的是 `CreateTemp` + `Rename`，所以不会留下半个目标文件（这点是好的）。
- **真正的问题**：`/shared` 目录下的源文件是**另一个服务（FFHub 主站）通过共享卷投放进来的**。
  这里用"移动"语义把主站的源文件搬走了。一旦后续 `CreateFileMeta`/`UpsertFileMetaBySlug`
  失败，`upload.go:126` 的 `RemoveOrphanBlob(sha512)` 会把刚搬进 storage 的 blob 删掉
  ——**结果：源文件既不在 shared，也不在 storage，数据彻底丢失，且主站对此毫不知情。**
  这是不可逆的数据丢失路径。
- 修复：默认改为 `ShouldCopy` 语义（或至少元数据写库成功后再删源文件）；
  `RemoveOrphanBlob` 在"入库失败"路径上不应删除刚搬入的 blob。

---

## 二、中等（逻辑错误 / 缓存不一致 / 性能）

### BUG-5【P1】缓存空值标记与"Redis 不可用"混为一谈，导致缓存幻影

- 位置：`internal/db/database.go:88-110`
- 问题：`GetCached` 在 Redis 报错（`!errors.Is(err, redis.Nil)`）时只是
  `slog.Warn` 后**照常回源查询**，这本身是合理的降级。但回源失败/无数据时，
  第 98 行与第 109 行 `R.Set(ctx, key, RedisEmptyMark, duration)` 的**返回值被丢弃**，
  `SetRedis` 同理（第 101 行）。
  更严重的是 `RedisEmptyMark = "<{NULL}>"` 这个**哨兵字符串直接和真实 JSON 共用一个 key 空间**，
  而 `GetCached`（第 77 行）用 `res == RedisEmptyMark` 做字符串比较。
  由于 `SetRedis` 用 `json.Marshal` 写入，**任何真实数据都不可能等于裸的 `<{NULL}>`**，
  所以当前不会误判——但这是一个极其脆弱的隐式约定：只要将来有人改用
  `json.Marshal("<{NULL}>")` 缓存一个字符串，或改动哨兵值，缓存就会静默返回
  "不存在"。建议给空值标记加独立前缀或改用 `[]byte` 魔法值。
- 另：`GetRedis`（第 22-47 行）与 `GetCached`（第 67-111 行）代码**几乎完全重复**，
  且两者对"Redis 报错"的处理策略相反（前者返回错误，后者降级回源），
  容易在维护时改错其中一处。建议合并。

### BUG-6【P1】`GetFileMetaBySlug` 的空值语义与 NotFound 语义冲突

- 位置：`internal/service/filemeta/filemeta.go:96-126`
- 问题：`GetCached` 在"数据库无此行"时返回 `(nil, nil)`，并把 `nil` 缓存 5 分钟
  （`database.go:109`）。`GetFileMetaBySlug` 因此返回 `NotFoundError()`。
  但 **`db.GetCached` 的通用签名 `(*T, *errs.Error)` 无法区分
  "缓存中说没有" 与 "真的没有"**，而 `GetFileMetaByID`（第 64-85 行）同样会把
  `nil` 当作"不存在"。
- 具体 BUG：**`CreateFileMeta` 之后写入的缓存 key 顺序有问题**——
  `filemeta.go:46-49` 先写 `FileMetaCacheKey(f.ID)`，再写 `FileMetaIDCacheKey(f.Slug.String)`。
  如果同一 slug 之前被查询过并缓存了 `RedisEmptyMark`（负缓存 5 分钟），
  **新建文件后该负缓存不会被清除**，`GetFileMetaBySlug` 在接下来最多 5 分钟内
  仍返回 404。`UpsertFileMetaBySlug`（第 204-205 行）删了这两个 key，
  但 `CreateFileMeta` **没有删除负缓存** —— 两条路径行为不一致，这是明确的遗漏。
- 修复：`CreateFileMeta` 中补 `db.R.Del(ctx, FileMetaIDCacheKey(f.Slug.String))`。

### BUG-7【P1】`DeleteFileMeta` / `UpdateFileMeta` 的缓存失效不完整

- 位置：`internal/service/filemeta/filemeta.go:143-163`、`214-233`
- 问题：
  1. `UpdateFileMeta` 先 `GetFileMetaByID` 拿到 `oldMeta`，但如果 `oldMeta == nil`
     （缓存命中空值标记，见 `database.go:77`），紧接着第 155 行 `oldMeta.Slug.Valid`
     **会 panic（nil 指针解引用）**。`GetCached` 在命中空标记时返回 `(nil, nil)`，
     `UpdateFileMeta` 只检查了 `getErr != nil`，**没有检查 `oldMeta == nil`**。
     这是一个可触发的空指针崩溃。
  2. `DeleteFileMeta` 删除了 `FileMetaCacheKey(id)` 和 slug→id 的 key，但
     **没有清理 `FileMetaCacheKey` 里可能存在的负缓存**——其实删了，OK；
     但它没有处理"该 sha512 是否还有其他行引用"的问题：删除元数据后
     **blob 永远不会被回收**（没有调用 `RemoveOrphanBlob`），磁盘只增不减。
  3. `DeleteFileMeta`、`UpdateFileMeta` 目前**没有任何调用方**（死代码），
     但既然存在就应按正确语义修好，否则将来接入时直接踩坑。

### BUG-8【P1】`DownloadSession` 的 slug 未做归一化，与路由参数不一致即鉴权失败

- 位置：`internal/handler/download/download.go:38-42` + `internal/handler/file/file.go:65,123`
- 问题：`createDownloadHandler` 直接存 `req.Slug` 原样字符串；`serveFile`/`fileInfo`
  比较 `s.Slug != slug`，而 `slug` 来自 `c.Param("slug")`（`/file/*slug` 通配）。
  Gin 的 `*slug` 通配参数**会带上前导 `/`**（例如请求 `/api/v1/file/foo` 得到 `"/foo"`），
  而调用方创建会话时传的 slug 是 `"/foo"`（`upload.go:45` 强制要求 slug 以 `/` 开头）
  —— 这两者恰好能对上，但**完全依赖"客户端传的 slug 恰好等于 URL 路径"这一隐式约定**。
  任意编码差异（`%2F`、多余斜杠、`/foo` vs `//foo`、大小写）都会导致**合法令牌被判为无效**，
  返回 401 而不是 404，排查困难。
- 修复：创建会话时对 slug 做 `path.Clean` 归一化，两侧统一后再比较。

### BUG-9【P1】上传接口的 slug 校验过弱

- 位置：`internal/handler/upload/upload.go:45-48`
- 代码：`if !strings.HasPrefix(req.Slug, "/") { ... }`
- 问题：只检查了前缀 `/`，因此 `"/"`、`"/../../etc/passwd"`、`"/a/../../b"`、
  `"/foo?x=1"`、`"/foo#frag"` 都会通过校验并**原样入库**。
  这些值会流进：
  - `file.go:101` 的重定向目标 `c.Request.URL.EscapedPath() + "?" + q.Encode()`；
  - `file.go:146` 的 `Content-Disposition` 头。
  虽然 `serveFile` 只按 `meta.SHA512` 取文件（不拼接路径，因此**没有路径穿越**），
  但难看的 slug 会破坏缓存键（`FileMetaIDCacheKey` 直接拼接原始 slug）并使 URL 无法稳定匹配。
  另：`CheckSlugExists` 存在但**从未被调用**，上传前的 slug 冲突检查缺失，
  冲突只能依赖 DB 唯一索引报 23505 → 走 `CreatePGError` 的 409 分支（且按 BUG-2 无日志）。
- 修复：用 `path.Clean(slug) == slug && slug != "/"` 做规范化校验，并限制长度与字符集。

### BUG-10【P2】`storage.FilePath` 对短字符串会 panic

- 位置：`internal/service/storage/storage.go:19-21`
- 代码：`return filepath.Join(FileDir, sha512[:2], sha512)`
- 问题：`sha512[:2]` 在 `len(sha512) < 2` 时**直接 panic（slice bounds out of range）**。
  当前调用点都传真实的 128 位十六进制摘要，但 `upload.go:61-70` 允许**客户端自行传入
  `req.SHA512`**（`if !req.SHA512.Valid { 计算 } else { sha512 = req.SHA512.String }`）
  ——**完全没有校验长度和十六进制格式**。传入 `"a"` 或 `"../x"` 时：
  - `"a"` → `FilePath` panic → 整个服务进程可能被 Gin 的 recovery 兜住，但若无 recovery 则崩溃；
  - 恶意/任意 sha512 会让 `storage.FilePath` 指向**任意相对路径**（未做 SafeJoin 校验），
    `os.MkdirAll(path.Dir(dst))` 与 `CopyFile/MoveFile` 的写入目标随之可控。
  **这是一条真实的高危路径**：`sha512` 必须强制校验 `^[0-9a-f]{128}$`。
- 修复：入口处用正则校验 `req.SHA512`；`FilePath` 加长度防御与 `SafeJoin`。

### BUG-11【P2】`RemoveOrphanBlob` 的 TOCTOU 与空 sha512 缺陷

- 位置：`internal/util/file/utils.go:123-144`
- 问题：
  1. `path := storage.FilePath(sha512)` 中当 `sha512 == ""` 时，`FilePath` **先 panic**
     （`[:2]`），**根本走不到**第 125 行的 `if path == "" { return }` 防御——
     这个判断是**死代码**，永远不可能为真（`filepath.Join` 不会返回空串）。
     防御写错了位置，实际无效。
  2. `CheckSHA512Exists` 与 `os.Remove` 之间存在 TOCTOU 窗口：并发上传同一 sha512 时，
     线程 A 刚查到"无引用"准备删除，线程 B 恰好插入元数据引用该 blob → A 把 B 的数据删了。
     上传接口用的是 `ShouldCopy`/`ShouldUpsert` 组合，这个窗口是可达的。
- 修复：`RemoveOrphanBlob` 开头改为 `if len(sha512) < 2 { return }`；删除与引用检查放到同一事务/用 DB 约束保证。

### BUG-12【P2】`CopyFile` 失败时错误被覆盖，丢失根因

- 位置：`internal/util/file/utils.go:39-49`、`50-60`
- 代码：
  ```go
  if _, err := io.Copy(out, in); err != nil {
      err := out.Close()          // ← 遮蔽了外层的 err
      ...
      err = os.Remove(tmpOut)     // ← 再次覆盖真正的失败原因
      ...
      return err                  // ← 返回的是 os.Remove 的结果，不是 io.Copy 的错误
  }
  ```
- 问题：`io.Copy` 的真实错误（如 `ENOSPC` 磁盘写满）被 `out.Close()` 和 `os.Remove()`
  的返回值**逐层覆盖**，函数最终返回 `os.Remove` 的结果（通常为 `nil`！）。
  **结果是：磁盘写满时 `CopyFile` 可能返回 `nil`（视 `Remove` 成功与否），
  调用方以为复制成功，实际文件不完整。** 两处（39-49、50-60）都有此问题。
- 修复：用 `defer` 做清理，保留并包装原始错误：
  ```go
  if _, err := io.Copy(out, in); err != nil {
      _ = out.Close(); _ = os.Remove(tmpOut)
      return fmt.Errorf("copy: %w", err)
  }
  ```

### BUG-13【P2】`MoveFile` 成功路径的返回值与错误包装不一致

- 位置：`internal/util/file/utils.go:71-81`
- 问题：`os.Remove(src)` 失败只 `slog.Warn` 后 `return nil`——**"移动"语义下源文件没删掉
  却报告成功**。若这是 `/shared` 中的源文件（见 BUG-4），主站会认为文件已被消费并删除，
  而实际上文件仍躺在共享目录里被重复处理。
  另外 `fmt.Errorf("copy to dst: %w", err)` 的文案与实际失败操作不符，误导排查。
  且第 77 行日志文案 `"Failed to remove temp file"` 是复制粘贴错误——删的是 `src` 不是 temp file。

### BUG-14【P2】`DetectContentType` 的 `head[:n]` 与 `io.ReadFull` 语义

- 位置：`internal/util/file/utils.go:115-120`
- 问题：`io.ReadFull` 失败时 `n` 是**已读字节数**，代码对 `io.EOF` / `io.ErrUnexpectedEOF`
  放行是正确的（空文件和短文件），这部分没问题。
  真正的问题是**该方法已废弃**：Go 1.20+ 起 `http.DetectContentType` 对无 `Content-Type`
  场景的启发式判断已知不可靠，而项目 go.mod 声明 `go 1.26.0`（第 3 行），
  且 `go.sum` 里已经间接依赖了 `github.com/gabriel-vasile/mimetype v1.4.15`
  （由 gin 引入）。既然依赖已在，应当改用 `mimetype.DetectFile` 提升准确率。
  另外**空文件**会得到 `"text/plain; charset=utf-8"`，对二进制上传是错误的内容类型，
  且这个错误值会被**永久写入数据库**（`upload.go:78`）。

### BUG-15【P1】CORS 中间件对 `OPTIONS` 预检请求的处理有漏洞

- 位置：`internal/router/router.go:35-42`
- 代码：
  ```go
  r.Use(func(c *gin.Context) {
      switch {
      case under(c.Request.URL.Path, "/api/internal"):
          internalCors(c)
      case under(c.Request.URL.Path, "/api/v1"):
          apiCors(c)
      }
  })
  ```
- 问题：`gin-contrib/cors` 的中间件**自身会调用 `c.Abort()` 并终止 OPTIONS 预检**，
  这里把它包在一个匿名中间件里执行。由于 `internalCors(c)` 内部会 `AbortWithStatus`，
  流程能正常中断；但**不匹配任何前缀的路径（例如 `/api/v10/...`、`/healthz`）不会设置任何 CORS 头**
  —— `under()` 的实现避免了 `/api/v1` 前缀误匹配 `/api/v10`，这点是对的。
  真正的问题是 **`apiCors := cors.Default()`**：默认配置 `AllowAllOrigins = true`（`*`），
  同时 `cors.Default()` 的 `AllowMethods` 只含 `GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS`，
  **不含 `X-Api-Key` 允许头**——而 `/api/v1` 下的接口若将来需要该头会预检失败。
  更关键的安全问题：`apiCors` 允许所有来源，**`/api/v1/info/*slug` 会暴露 `user_id`/`app_id`/
  `sha512` 等元数据给任意站点**（配合 `AllowCredentials` 缺失时为只读泄露）。
  而 `internalCors` 的 `AllowOriginFunc` 用
  `strings.HasSuffix(origin, ".fdfz.top")` 判断——**`https://evil.fdfz.top` 会被放行**，
  若存在任意子域被接管的场景即可绕过。且 `AllowCredentials: true` 与后缀匹配组合放大了风险。
- 修复：`/api/v1` 改为显式白名单；`AllowOriginFunc` 改为精确的完整域名集合匹配。

### BUG-16【P1】健康检查路由未使用任何中间件，且 `db.R` 可能为 nil

- 位置：`internal/handler/healthcheck/healthcheck.go:42-93`
- 问题：
  1. `main.go:91` 创建的 `healthcheckR := gin.New()` **没有 `gin.Recovery()`**，
     而主路由 `r` 同样只有 `sloggin.New(logger)`（`main.go:92`），
     **两个 Engine 都没有挂 `gin.Recovery()`**。任何 handler 中的 panic
     （例如 BUG-7 的 nil 解引用、BUG-10 的 slice 越界）都会**直接杀掉整个连接**，
     `gin.New()` 不像 `gin.Default()` 那样自带 Recovery。
     **这是一个全站级的健壮性缺陷。**
  2. `check()` 中 `db.R.PDB == nil` 的判断（第 46 行）在 `db.R` 本身为 nil 时会 panic。
     `main.go:78` 确实在迁移前就赋了值，但只要 `RunMigrations` 之后有任何路径重置 `db.R`
     就会崩。防御应写成 `db.R == nil || db.R.PDB == nil`。
  3. `check` 函数内局部变量 `check := Check{}` **遮蔽了同名函数 `check`**（第 27 行定义），
     可读性差，容易在后续维护中误调用。`go vet` 不报，但是真实的可维护性缺陷。
  4. `allUp` 变量在 goroutine 中经 `mu` 保护写入，**但读取在第 97 行 `wg.Wait()` 之后**，
     此时已无并发，逻辑正确——不过 `timeout = 10s` 是包级可变全局变量，
     被测试并发修改时会有数据竞争。

### BUG-17【P1】`ServeContent` 与预压缩/Cache-Control 的组合缺陷

- 位置：`internal/handler/file/file.go:145-154`
- 问题：
  1. 私密文件设置了 `Cache-Control: private, no-store`，但 **`http.ServeContent` 会依据
     `meta.CreatedAt` 生成 `Last-Modified` 并可能返回 `304 Not Modified`**。
     客户端在令牌失效后用条件请求仍可能拿到 304（复用本地缓存），**削弱了 `no-store` 的意图**。
  2. 公开文件设置 `max-age=31536000, immutable` + 第 97-105 行的 `?v=<sha512>` 强制重定向。
     逻辑是自洽的（内容寻址），但**第 102 行设置的 `Cache-Control: no-cache` 会被随后
     第 151 行的 `public, max-age=31536000, immutable` 覆盖吗？** 不会——重定向分支在
     第 104 行就 `return` 了，所以 302 响应带的是 `no-cache`，这是正确的。
     **真正的问题**：`c.Header("Content-Type", meta.ContentType)` 直接使用**用户可控**的值
     （`upload.go:72-79` 允许客户端通过 `content_type` 字段任意指定，且不做任何校验）。
     攻击者可上传 `content_type: "text/html"` 的文件，配合 `Content-Disposition: inline`
     → **存储型 XSS**（在 `*.fdfz.top` 域下执行 JS，可窃取同域 cookie）。
     **这是本次审查中最高危的安全问题。**
  3. `mime.FormatMediaType("inline", ...)` 对 `meta.Filename` 中的引号/换行做了转义，这点是安全的；
     但 `filename` 非 ASCII 时会退化为 `filename*=utf-8''...`，部分客户端下载文件名异常。

### BUG-18【P1】下载会话创建接口缺少 slug 存在性校验

- 位置：`internal/handler/download/download.go:25-50`
- 问题：`createDownloadHandler` 接受任意 `Slug`，**从不校验该 slug 是否存在于数据库**，
  直接生成并返回一个有效期 5 分钟的令牌（`cfg.C.DownloadTTL`）。
  后果：任何人都能（持 API Key 时）批量生成无限量的无效令牌，**Redis 被垃圾数据填满**，
  且没有速率限制。应在创建前调用 `filemeta.GetFileMetaBySlug` 做一次存在性校验，
  顺便可以判定是否 `IsPrivate`（当前连私密文件也能拿到令牌，语义模糊）。

---

## 三、轻微（代码质量 / 配置 / 一致性）

### BUG-19【P2】`errs.CreateAndLogInternalError` 的错误信息全部丢失

- 位置：`internal/errs/error.go:62-65`
- 代码：
  ```go
  func CreateAndLogInternalError[T any](err T, log string, args ...any) *Error {
      slog.Error(log, append([]any{"error", err}, args...)...)
      return InternalError()
  }
  ```
- 问题：泛型 `T` 允许传入**非 error 类型**（编译期无法约束），日志用 `slog` 的
  "key-value 序列"写法，`append([]any{"error", err}, args...)` 在 `args` 为奇数个时
  会产生 `!BADKEY` 输出。建议签名改为 `err error` 并加一个 `attrs ...slog.Attr` 变参。

### BUG-20【P2】`cfg` 的 `requireEnv` 与 `API_KEY` 检查存在冗余/矛盾

- 位置：`internal/cfg/config.go:31-35`
- 代码：
  ```go
  if cfg.APIKey, err = requireEnv("API_KEY"); err != nil {
      return err
  } else if len(cfg.APIKey) == 0 {
      return fmt.Errorf("API key not set")
  }
  ```
- 问题：`requireEnv` 用 `os.LookupEnv`，**只要变量存在（哪怕值为空串）就返回成功**，
  所以第二个检查确实有用——但错误信息 `"API key not set"` 与实际情形
  （"已设置但为空"）不符。更重要的是**没有校验 API Key 强度**，
  `.env.example` 里的 `change-me-to-a-long-random-string` 这种占位值会被直接接受并用于鉴权。
- 另：`envDur`（第 57-62 行）在 `os.Getenv` 为空时 `time.ParseDuration("")` 报错，
  正确回退默认值，逻辑无误；但**非法值（如 `DOWNLOAD_TTL=abc`）被静默忽略，
  没有任何告警**，运维会以为配置生效了。

### BUG-21【P2】`.env.example` 的 `SHARED_DIR` 为空、Dockerfile 的 ldflags 路径错误　—— ✅ ldflags 已修复，已验证

- 位置：`.env.example:4`、`Dockerfile:16-17`
- 问题：
  1. `Dockerfile` 的 ldflags 指向 **`ffhub-filestore/internal/config`**（第 16-17 行），
     而实际包路径是 **`ffhub-filestore/internal/cfg`**（`internal/cfg/version.go`）。
     `-X` 对不存在的包**静默失败**，因此 `/api/v1/version` 返回的 `commit`/`build_time`
     **永远是默认值 `"dev"`**。CI 传的 `--build-arg COMMIT=...` 完全没生效。
     **这是一个确定无疑的构建 BUG。**
  2. `.env.example` 里 `SHARED_DIR=`（空值），而 `cfg.env()` 对空字符串回退到 `./shared`，
     所以行为上没问题，但示例文件为空值容易让人误以为要填绝对路径。

### BUG-22【P2】Dockerfile 的 `HEALTHCHECK` 依赖 curl，但 `apk del tzdata` 写法有风险

- 位置：`Dockerfile:26-29, 42-43`
- 问题：`apk add --no-cache ca-certificates tzdata curl && ... && apk del tzdata`
  删除 tzdata 后 `/usr/share/zoneinfo/Asia/Shanghai` 的来源被移除（文件已复制所以仍可用），
  但 `echo "Asia/Shanghai" > /etc/timezone` 在 Alpine（musl）下**并不被 Go 运行时读取**
  ——Go 依赖 `TZ` 环境变量或 `/etc/localtime`。当前靠 `cp` 复制 `/etc/localtime` 生效，
  `/etc/timezone` 是 Debian 系的做法，此处无效（无害但误导）。
  另：`HEALTHCHECK` 打的是 `8080`，而 `EXPOSE` 只声明了 `11410`
  （第 40 行），**8080 未 EXPOSE**，编排文件若依赖 EXPOSE 会探测不到。

### BUG-23【P2】Woodpecker CI 的 `set-version` 步骤产物无法跨步骤传递

- 位置：`.woodpecker/build-and-deploy.yaml:6-10`
- 问题：`set-version` 步骤写 `.env` 到**该步骤容器的临时工作区**，
  Woodpecker 的步骤之间**不共享文件系统**（除非配置 workspace 卷），
  因此 `build`/`deploy` 步骤里的 `. .env`（第 19、36 行）**大概率读不到文件**，
  `$REL_VERSION` 为空 → 镜像 tag 变成 `fdfzers/ffhub-filestore:`（非法），
  `sed` 会把 compose 里的 image 替换成空值。
  另外第 41 行 `sed -i "s|image:.*|image: $IMAGE_NAME|" $COMPOSE_FILE`
  **会替换文件中所有匹配行**（compose 里若有多个 service 会把别的服务镜像也改掉）。
- 修复：改用 Woodpecker 的 `environment:`/`depends_on` + 环境变量传递，
  `sed` 限定目标行或改用 `docker compose` 的 `--env-file`。

### BUG-24【P2】`main.go` 的启动顺序与错误处理缺陷

- 位置：`cmd/server/main.go`
- 问题：
  1. **第 42 行的日志在第 46 行加载 `.env` 之前打印**，所以日志里 `is_prod` 的取值
     来自 `version.go` 的包级变量初始化时机（`cfg.IsProd` 在 import 时求值），
     而 `godotenv.Load()` 是**在第 42 行之后**才执行的——**`APP_ENV=prod` 写在 `.env` 里时永远不会生效**，
     `IsProd` 恒为 `false`（除非用真实环境变量）。这会导致：
     非 prod 分支执行 `godotenv.Load()` + 调试日志 + `tint` 彩色输出进入生产。
     **这是确定的功能性 BUG。**
  2. **`healthcheckSrv` 只在 `cfg.IsProd` 时才启动**（第 127-134 行），
     但 `Dockerfile` 第 45 行强制 `ENV APP_ENV=prod`，所以生产能跑通；
     不过在本地/非 prod 下 `:8080` 完全不监听，而 `Dockerfile` 的 `HEALTHCHECK`
     又依赖它——**如果用 `docker run` 覆盖 `APP_ENV`，容器会永远 unhealthy**。
  3. 第 152 行在 `srv.Shutdown(ctx)` **已经消耗掉 10 秒超时**之后，
     复用**同一个 ctx** 去 `healthcheckSrv.Shutdown(ctx)`——此时 ctx 已过期，
     healthcheck 服务器不会优雅关闭。需要一个独立的超时 context。
  4. `srv` 启动的 goroutine 里 `os.Exit(1)`（第 123 行）会**跳过所有 defer 和优雅关闭**，
     直接退出进程。应改为向 `quit` channel 发信号或至少 `slog.Error` 后显式关闭资源。
  5. `pdb.Close()`（第 157 行）没有 defer 保护，若中间 panic 则连接池不释放。

### BUG-25【P2】`GetRedis` 命中空标记时无法返回"确实不存在"与"解析失败"的区别

- 位置：`internal/db/database.go:22-47`
- 问题：`GetRedis[T]` 在 `json.Unmarshal` 失败时返回 `errs.Error`，
  而 `session.GetDownloadSessionByToken`（`session/download.go:31-37`）直接把该错误
  透传给 handler，`file.go:60-63` 与 `118-121` 处 `err.AppendDetails(...)` 后 `Respond` ——
  此时 `err` 的 `HTTPCode` 是内部错误的 500（`CreateAndLogInternalError` 返回 `InternalError()`），
  **把"令牌格式损坏"这种客户端问题报成了 500**。而下一个分支
  `if s == nil || s.Slug != slug` 才返回 401，**错误码不统一**。
  正常情况（令牌不存在）走 `redis.Nil` → 返回 `(nil, nil)` → 401，是正确的；
  只有缓存数据损坏这一罕见路径会返回 500。

### BUG-26【P3】路由与接口设计一致性问题

- 位置：`internal/router/router.go:20-51`
- 问题：
  1. `internal` 组下的 `GET /api/internal/` 与 `/api/internal`（`misc.go:17`）——
     Gin 对带/不带尾斜杠的处理依赖 `RedirectTrailingSlash`（默认 true），
     会 301 重定向，**对 POST `/api/internal/upload/store` 这类接口，
     若客户端多发一个尾斜杠会变成 307/301 重定向**（Gin 对非 GET 用 307 保方法，尚可）。
  2. `/api/v1` 下**没有任何鉴权**，`/info/*slug` 会泄露 `user_id`、`app_id`（见 BUG-15）。
  3. `under()` 辅助函数（第 16-18 行）逻辑正确，避免了 `/api/v1` 匹配 `/api/v10`，
     但 `cors.Default()` 与其他 CORS 配置混用，风格不统一。

### BUG-27【P3】`model.FileMetadata.Slug` 序列化与 DB 的 NULL 语义

- 位置：`internal/model/model.go:11`、`internal/service/filemeta/filemeta.go:33-36`
- 问题：`createFileMetaSQL` 插入时使用 `f.Slug`（`null.String`），
  但 `upload.go:108` 已经强制 `null.StringFrom(req.Slug)`（必非 NULL），
  所以 DB 里 `slug` 列**永远不会是 NULL**，而迁移 `000001` 却定义了
  `slug TEXT UNIQUE` + 部分唯一索引 `WHERE slug IS NOT NULL`（第 17-19 行），
  upsert 也用 `ON CONFLICT (slug) WHERE slug IS NOT NULL`。
  **这部分设计是为 NULL 准备的，但代码路径不会产生 NULL**，属于冗余；
  更麻烦的是 `files_slug_key` 唯一索引与列级 `UNIQUE` 约束**重复定义**（第 9 行 + 第 17-19 行），
  Postgres 会创建两个等价索引，浪费空间并可能让 `pgErr.ConstraintName` 出现两种取值
  （`file_metas_slug_key` vs `files_slug_key`），导致 BUG-2 的 409 提示不稳定。

### BUG-28【P3】缺少任何测试

- 位置：整个仓库
- 问题：**没有任何 `_test.go` 文件**。`fileutils.SafeJoin`（安全关键）、
  `crypto.GenerateCode`、`errs.CreatePGError`、`db.GetCached` 的缓存语义
  都是高风险且易测的纯逻辑，应当优先补单测。
  建议同时接入 `go test -race ./...` 到 Woodpecker 流水线。

### BUG-29【P3】`.gitignore` / `.dockerignore` 遗漏

- 问题：`.gitignore` 忽略了 `/storage`（第 45 行）但**没有忽略 `shared/`**
  ——`.env.example` 默认 `SHARED_DIR=./shared`，本地开发时上传的源文件会被 git 跟踪。
  `.dockerignore` 同理，`docker build` 会把本地 `shared/` 整个打进构建上下文（镜像体积与信息泄露）。

### BUG-30【P3】日志与可观测性

- 问题：全局没有任何请求 ID / trace 关联；`sloggin.New(logger)` 是唯一中间件；
  错误响应体 `ErrorInfo` 只有字符串，**不返回任何可用于对照日志的 correlation id**，
  线上用户报错时无法定位具体请求（配合 BUG-2 的无日志，排查链路完全断裂）。

---

## 四、建议的修复优先级

| 优先级 | 编号 | 说明 |
| --- | --- | --- |
| ~~P0~~ 已完成 | ~~BUG-1~~ | ~~上传 404 报成 500，且 `os.Stat` 其它错误未处理~~ ✅ |
| ~~P0~~ 已完成 | ~~BUG-21.1~~ | ~~Dockerfile ldflags 包路径错误，版本信息永远是 `dev`~~ ✅ |
| P0（立即） | BUG-17 | `Content-Type` 用户可控 + `inline` → 存储型 XSS |
| P0（立即） | BUG-10 | 未校验的 `sha512` → panic / 任意路径写入 |
| P0（立即） | BUG-16 | 两个 `gin.Engine` 都没有 `Recovery()`，任何 panic 杀连接 |
| P0（立即） | BUG-24.1 | `.env` 在 `IsProd` 求值之后才加载，prod 模式永远不生效 |
| P0（立即） | BUG-7.1 | `UpdateFileMeta` 对 `oldMeta == nil` 解引用会 panic |
| P1（本周） | BUG-2, BUG-3, BUG-4, BUG-12 | 数据库错误无日志；令牌顺序错误；数据丢失；错误被覆盖 |
| P1（本周） | BUG-6, BUG-15, BUG-18 | 负缓存不失效；CORS 过宽；令牌无限生成 |
| P1（本周） | BUG-23 | CI 变量传递失败，部署会打空 tag |
| P2（排期） | 其余 | 代码质量、一致性、资源回收 |

---

## 附：已验证「不是 bug」的疑点

为避免误报，以下几处经过实际核查后确认**没有问题**：

1. **`internal/db/postgres.go` 的迁移与 pgx 预编译语句冲突**：曾怀疑
   `ALTER TABLE ... ADD COLUMN`（迁移 000002）与 `RunMigrations` 中
   `migration.NewWithSourceInstance` 使用的 pgx 连接会触发
   PostgreSQL 的 `cached plan must not change result type (0A000)`。
   核查 pgx v5.11.0 源码（`conn.go:320-378`）后确认：
   当 `name == ""` 时走 `psKey = name = ""` 分支，**不会写入
   `c.preparedStatements` 缓存**，因此迁移 DDL 不会污染应用连接的语句缓存，
   且两者使用独立连接池。该疑点不成立。
2. **`migrate.Close()` 的 `slog.Warn` 无条件打印**（`postgres.go:69-72`）：
   看起来像"正常关闭也报警告"的 bug，但实际上是**纯粹的日志噪音**，
   `sourceErr`/`dbErr` 为 nil 时 `slog.Warn` 会打印 `sourceErr=<nil> dbErr=<nil>`，
   属于可读性问题（建议改为仅在非 nil 时记录），不是功能缺陷。
3. **`Count`/`wg.Go` 用法**：`healthcheck.go:42` 使用的是 Go 1.25+ 的
   `sync.WaitGroup.Go` 方法，与 `go.mod` 声明的 `go 1.26.0` 匹配，编译通过，不是 bug。
4. **`for i := range length`**（`crypto/code.go:14`）：Go 1.22+ 的
   range-over-int 语法，正确。
5. **`for p := range strings.SplitSeq(...)`**（`cfg/config.go:37`）：
   Go 1.24+ 的迭代器语法，正确。
6. **`Content-Disposition` 头注入**：`mime.FormatMediaType` 会对 `\r\n` 和引号做转义，
   `meta.Filename` 无法注入额外响应头。该点安全。
7. **`serveFile` 路径穿越**：文件路径完全由 `meta.SHA512`（数据库值）决定，
   **不使用** URL 中的 slug，因此 `slug` 无法造成目录穿越。
   （但 `req.SHA512` 客户端可控的问题见 BUG-10。）
