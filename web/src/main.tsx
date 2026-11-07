import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/tokens.css";
import App from "./App";

// document.hidden 禁动画由 tokens.css 全局规则消费,此处维护标记
document.addEventListener("visibilitychange", () => {
  if (document.hidden) document.documentElement.setAttribute("data-document-hidden", "");
  else document.documentElement.removeAttribute("data-document-hidden");
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
