import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

function App() {
  const [url, setUrl] = useState('http://127.0.0.1:7860');

  useEffect(() => {
    const api = (window as any).go?.main?.App;
    if (!api?.OpenWebUI) {
      return;
    }
    api.OpenWebUI().then((next: string) => {
      if (next && next.startsWith('http')) {
        setUrl(next);
      }
    });
  }, []);

  return <iframe title="cftunnelX WebUI" src={url} />;
}

createRoot(document.getElementById('root')!).render(<App />);
