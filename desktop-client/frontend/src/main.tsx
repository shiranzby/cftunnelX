import React, { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

function App() {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [url, setUrl] = useState('http://127.0.0.1:7860');
  const [status, setStatus] = useState('正在连接 WebUI...');

  const syncTheme = () => {
    try {
      const theme = iframeRef.current?.contentDocument?.documentElement?.dataset.theme || 'light';
      document.documentElement.dataset.theme = theme;
    } catch {
      document.documentElement.dataset.theme = 'light';
    }
  };

  const openWeb = async () => {
    const api = (window as any).go?.main?.App;
    if (api?.OpenWebUI) {
      const next = await api.OpenWebUI();
      if (next && next.startsWith('http')) {
        setUrl(next);
        setStatus('WebUI 已启动');
      } else {
        setStatus(next || 'WebUI 启动失败');
      }
      return;
    }
    setStatus('当前为浏览器预览模式');
  };

  useEffect(() => {
    openWeb();
    const timer = window.setInterval(syncTheme, 700);
    return () => window.clearInterval(timer);
  }, []);

  return (
    <main>
      <header>
        <div className="logo">X</div>
        <div>
          <h1>cftunnelX</h1>
          <p>{status}</p>
        </div>
        <div className="spacer" />
        <button onClick={openWeb}>重新连接</button>
        <button className="ghost" onClick={() => window.open(url, '_blank')}>浏览器打开</button>
      </header>
      <iframe ref={iframeRef} title="cftunnelX WebUI" src={url} onLoad={syncTheme} />
    </main>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
