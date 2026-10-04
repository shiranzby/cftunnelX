package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

// loginPage 是在浏览器原生 Basic Auth 弹窗之外提供的自绘登录页。
//
// 背景：原先用 WWW-Authenticate: Basic 触发浏览器内置弹窗。该弹窗无法
//   - 适配站点深/浅色主题（永远跟随系统或浏览器默认）
//   - 展示品牌信息，用户无法判断自己在给哪个服务输入凭据
//   - 在 Safari / 部分移动端表现割裂
// 因此改为：页面请求直接返回自绘登录页，登录成功后由前端写入凭据再重放。
//
// 主题适配用 CSS 的 prefers-color-scheme，无需 JS 探测，也能在未加载完时正确着色。
const loginPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>登录 · cftunnelX</title>
<style>
:root{
  --bg:#f7f9fc; --card:#ffffff; --text:#172033; --muted:#6b7688; --sub:#99a5b8;
  --line:#e7edf5; --blue:#2563eb; --red:#dc2626; --shadow:0 12px 28px rgba(15,23,42,.06);
  color-scheme:light;
}
@media (prefers-color-scheme:dark){
  :root{
    --bg:#10151f; --card:#192231; --text:#eef4ff; --muted:#a5b1c4; --sub:#758398;
    --line:#2b3546; --blue:#60a5fa; --red:#f87171; --shadow:0 14px 34px rgba(0,0,0,.25);
    color-scheme:dark;
  }
}
*{box-sizing:border-box}
html,body{height:100%;margin:0}
body{
  font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Arial,sans-serif;
  background:var(--bg); color:var(--text);
  display:flex; align-items:center; justify-content:center;
  padding:24px 16px calc(24px + env(safe-area-inset-bottom));
}
.card{
  width:100%; max-width:380px; background:var(--card); border:1px solid var(--line);
  border-radius:16px; box-shadow:var(--shadow); padding:32px 28px; text-align:center;
}
/* 与主页面 .logo 保持一致：白底、圆角、轻投影，深色下用浅底以免糊成一团 */
.ico{
  width:52px; height:52px; margin:0 auto 18px; border-radius:12px;
  display:grid; place-items:center; background:#fff; overflow:hidden;
  box-shadow:0 8px 18px rgba(37,99,235,.14);
}
.ico img{width:100%;height:100%;object-fit:contain;border-radius:12px}
@media (prefers-color-scheme:dark){.ico{background:#fff}}
h1{margin:0 0 6px; font-size:20px; font-weight:800; letter-spacing:.2px}
.sub{margin:0 0 24px; font-size:13px; color:var(--muted)}
label{display:block; text-align:left; font-size:12px; font-weight:700;
  color:var(--muted); margin:0 0 7px}
input{
  width:100%; height:44px; margin-bottom:16px; padding:0 13px;
  font-size:15px; font-family:inherit; color:var(--text);
  background:var(--bg); border:1px solid var(--line); border-radius:10px;
  transition:border-color .15s,box-shadow .15s;
}
input:focus{outline:none; border-color:var(--blue);
  box-shadow:0 0 0 3px rgba(37,99,235,.15)}
button{
  width:100%; height:44px; border:0; border-radius:10px; cursor:pointer;
  font-size:15px; font-weight:800; font-family:inherit; color:#fff;
  background:var(--blue); transition:opacity .15s;
}
button:disabled{opacity:.6; cursor:default}
button:hover:not(:disabled){opacity:.9}
.err{
  display:none; margin:0 0 16px; padding:10px 12px; border-radius:9px;
  font-size:13px; text-align:left; color:var(--red);
  background:rgba(220,38,38,.09); border:1px solid rgba(220,38,38,.22);
}
.err.on{display:block}
.foot{margin:20px 0 0; font-size:12px; color:var(--sub)}
</style>
</head>
<body>
<form class="card" id="f" method="post" action="/api/session" autocomplete="on">
  <div class="ico"><img src="/assets/logo.png" alt="cftunnelX"></div>
  <h1>cftunnelX 控制台</h1>
  <p class="sub">需要登录后才能访问</p>
  <div class="err" id="e" role="alert"></div>
  <label for="u">账号</label>
  <input id="u" name="username" autocomplete="username" autocapitalize="off"
         autocorrect="off" spellcheck="false" required>
  <label for="p">密码</label>
  <input id="p" name="password" type="password" autocomplete="current-password" required>
  <button id="b" type="submit">登录</button>
  <p class="foot" id="m"></p>
</form>
<script>
(function(){
  var e=document.getElementById('e'),b=document.getElementById('b');
  // 原因由服务端注入，避免暴露过多内部信息
  var reason=%REASON%;
  if(reason){e.textContent=reason;e.className='err on'}
  // 提交中禁用按钮，避免重复提交
  document.getElementById('f').addEventListener('submit',function(){
    b.disabled=true;b.textContent='正在登录…';
  });
  document.getElementById('u').focus();
})();
</script>
</body>
</html>`

// renderLoginPage 输出自绘登录页，reason 会被 JSON 转义后注入页面。
func renderLoginPage(w http.ResponseWriter, reason string) {
	reasonJSON, err := json.Marshal(reason)
	if err != nil {
		reasonJSON = []byte(`""`)
	}
	page := strings.ReplaceAll(loginPage, "%REASON%", string(reasonJSON))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 登录页必须能被浏览器缓存之外地获取，且不触发中间代理缓存
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(page))
}
