// 占位的运行时配置，避免本地 vite dev 或部署 entrypoint 还没渲染时 404。
// 真正部署时由容器 entrypoint 用环境变量覆盖此文件。
window.__BETAGO_CONFIG__ = window.__BETAGO_CONFIG__ || {};
