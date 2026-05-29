# UI 设计规范

> 适用范围：`frontend/src/` 下所有 React 组件与样式文件  
> 定位：暗色工业终端风格（Dark Terminal），以可读性和信息密度为优先，不追求装饰。

---

## 1. 色彩体系

### 1.1 背景层级

| 用途 | 色值 |
|------|------|
| 全局底层背景 | `#0d0d0d` |
| 标题栏 | `#080808` |
| 过滤栏 / 状态栏 | `#0a0a0a` |
| 配置横幅 | `#0b0f0b` |
| 弹窗 / 模态框 | `#0f0f0f` |
| 输入框 / 普通卡片 | `#111` |
| 数据库连接卡片 | `#0d0d12` |

背景层从外到内依次加深，通过微妙的深度差体现层级关系。

### 1.2 分割线 / 边框

| 用途 | 色值 |
|------|------|
| 主分割线（面板间） | `#1a1a1a` |
| 普通边框 | `#222` |
| 输入框边框 | `#2a2a2a` |
| 卡片边框 | `#252530` |
| 浅色分隔符（行内） | `#333` |

### 1.3 文本层级

| 用途 | 色值 |
|------|------|
| 主标题 / 重要内容 | `#e0e0e0` |
| 正文 / 日志内容 | `#ccc` |
| 次要标签 | `#888` |
| 弱化说明 | `#666` |
| 占位 / 极淡 | `#555` |
| 最淡（不可见区域） | `#444` |

### 1.4 模块主题色

每个功能模块对应固定的主题色，**全局一致**，不可随意换色：

| 模块 | 主色 | 用途 |
|------|------|------|
| SCADA | `#4caf50` | 标签、状态点、区段标题 |
| Menu | `#2196f3` | 标签、状态点、区段标题 |
| HTTP | `#9c27b0` | 标签、区段标题 |
| App  | `#ff9800` | 标签、区段标题 |
| DB   | `#f44336` | 标签、区段标题 |
| Batch | `#ff9800` | 标签（与 App 共用橙色） |

### 1.5 日志级别色

| 级别 | 色值 |
|------|------|
| info | `#e0e0e0` |
| warn | `#f5c518` |
| error | `#ff4444` |

### 1.6 日志行背景（来源着色）

日志行按来源进行极浅的背景着色，增强视觉分区：

| 来源 | 行背景 |
|------|--------|
| scada | `#0d1a0d` |
| menu  | `#0d0d1a` |
| http  | `#1a0d1a` |
| app   | `#1a1a0d` |
| db    | `#1a0d0d` |
| batch | `#1a1a0d` |

奇偶行交替时，将上述色值各通道减 6（10 进制），产生轻微深浅交替效果。

### 1.7 透明度叠加约定

主题色常与透明度组合使用，以下是标准后缀：

| 用途 | 透明度后缀 |
|------|----------|
| 区段标题下划线 | `33`（约 20%） |
| 左边框装饰线 | `22`（约 13%） |
| 数据库连接名 Badge 背景 | `22` |
| 弱化高亮按钮背景 | 主色加深版（如 `#1a2a3a`） |

---

## 2. 字体排版

### 2.1 字体族

```css
font-family: "Consolas", "JetBrains Mono", "Noto Sans SC", monospace;
```

- 等宽字体优先，保证日志时间戳、标签对齐
- 中文备选：`Noto Sans SC`，避免系统黑体造成视觉割裂

### 2.2 字号体系

| 场景 | 字号 |
|------|------|
| 超小标签 / Badge | `10px` |
| 次要 UI（状态栏、过滤栏、表单标签、按钮） | `11px` |
| 通用 UI（输入框值、弹窗内文字） | `12px` |
| 日志内容行 | `12.5px` |
| 主标题 / 弹窗标题 | `13px` |

> 不应出现 14px 以上的字号，界面定位为紧凑型运维终端。

### 2.3 字重与间距

- 标签、区段标题使用 `fontWeight: 'bold'`
- 模块标签（TAG）加 `letterSpacing: '0.05em'`，区段标题加 `letterSpacing: '0.08em'`
- 日志行 `lineHeight: 1.6`

---

## 3. 间距体系

采用 4px 基准单位（4-point grid），常用值：

| 用途 | 值 |
|------|-----|
| 元素内间距（密集行） | `2px 10px` |
| 元素内间距（普通行） | `4px 12px` |
| 组件内间距（卡片、弹窗） | `8px 12px` ~ `20px 24px` |
| 同行元素 gap | `4px` / `8px` / `10px` / `16px` |
| 区块间 gap | `8px` / `16px` |

---

## 4. 圆角规范

| 场景 | 圆角 |
|------|------|
| 按钮（小型） | `3px` |
| 按钮（普通） / 输入框 | `4px` |
| 卡片 / 表单分组 | `6px` |
| 弹窗 / 模态框 | `8px` |
| Badge 小标签 | `3px` |
| 状态点（圆形） | `50%` |
| 切换开关轨道 | `10px` |
| 切换开关滑块 | `50%` |

---

## 5. 组件规范

### 5.1 按钮

```js
// 基础样式模板
{
  borderRadius: '4px',
  padding: '5px 16px',
  fontSize: '12px',
  cursor: 'pointer',
  border: '1px solid',
  transition: 'opacity 0.15s',
}
```

按钮分为三种语义：

| 类型 | 背景 | 边框色 | 文字色 | 用途 |
|------|------|--------|--------|------|
| 主操作（保存） | `#1a3a1a` | `#4caf50` | `#4caf50` | 确认/保存 |
| 次要操作（取消） | `#111` | `#333` | `#666` | 关闭/取消 |
| 危险操作（删除） | `#2a1a1a` | `#443` | `#f44336` | 删除 |
| 工具按钮（重连等） | `#1a1a2a` | `#333` | `#90caf9` | 次级功能 |
| 添加操作 | `#1a2a3a` | `#2196f3` | `#90caf9` | 新增条目 |

- `hover` 时全局统一降低 `opacity: 0.85`
- `active` 时 `opacity: 0.7`
- 禁用（saving 状态）时用主色的 `88` 透明度文字 + 更暗背景

### 5.2 输入框

```js
const inputStyle = {
  background: '#111',
  border: '1px solid #2a2a2a',
  color: '#ccc',
  borderRadius: '4px',
  padding: '5px 8px',
  fontSize: '12px',
  outline: 'none',
  width: '100%',
  boxSizing: 'border-box',
};
```

- 不显示原生 outline，边框即焦点表现
- select 与 input 保持统一样式

### 5.3 模态弹窗

```
遮罩: background rgba(0,0,0,0.75), position fixed, inset 0, zIndex 1000
弹窗: background #0f0f0f, border 1px solid #222, borderRadius 8px
      padding 20px 24px, maxHeight 90vh, overflowY auto
      scrollbarWidth thin, scrollbarColor #2a2a2a #0f0f0f
      width 520px（固定宽度）
```

- 点击遮罩区域关闭弹窗
- 内容区使用 flex column + `gap: 16px` 分隔各区段

### 5.4 区段标题（Section Header）

```js
{
  fontSize: '11px',
  fontWeight: 'bold',
  color: <模块主题色>,
  letterSpacing: '0.08em',
  borderBottom: `1px solid ${color}33`,
  paddingBottom: '4px',
}
```

### 5.5 切换开关（Toggle Switch）

```
轨道: width 36px, height 20px, borderRadius 10px
      背景: 开启 #1a5c1a / 关闭 #1a1a1a
      边框: 开启 #4caf50 / 关闭 #333
滑块: width 14px, height 14px, borderRadius 50%
      left: 开启 17px / 关闭 2px, top 2px
      背景: 开启 #4caf50 / 关闭 #444
      transition: all 0.2s
```

### 5.6 自定义复选框

```
容器: display flex, alignItems center, gap 8px, cursor pointer
      padding 6px 8px, borderRadius 4px
      背景: 选中 #1a1a0d / 未选中 #111
      边框: 选中 #ff9800 / 未选中 #252530
勾框: width 14px, height 14px, borderRadius 3px, flexShrink 0
      背景: 选中 #ff9800 / 未选中 transparent
      边框: 2px solid (选中 #ff9800 / 未选中 #555)
勾号: color #000, fontSize 10px, fontWeight bold
```

### 5.7 状态指示点

```js
{
  width: '7px',  // 状态栏用 7px，日志行用 5px
  height: '7px',
  borderRadius: '50%',
  background: ok ? '#4caf50' : '#f44336',  // SCADA
  background: ok ? '#2196f3' : '#555',     // 数据库
  display: 'inline-block',
}
```

### 5.8 日志行

```
容器: display flex, gap 8px, alignItems baseline
      padding 2px 10px
      borderLeft: 2px solid <tagColor>22（有连接名时用 connColor+99）
      交替行背景（偶数行背景 / 奇数行背景减 6）

列顺序:
  [时间戳]  color #555, fontSize 11px, flexShrink 0
  [TAG]     模块主题色, fontSize 10px, bold, minWidth 38px, textAlign right
  [连接名]  connColor, fontSize 10px, bold, padding 0 4px, borderRadius 3px, bg connColor+22
  [消息体]  fg levelColor, flex 1, wordBreak break-all
  [复制按钮] fontSize 11px, padding 0 4px, hover 变绿 #4caf50
```

### 5.9 配置横幅（Config Banner）

```
背景: #0b0f0b, borderBottom 1px solid #1a2a1a
字号: 11px, color #555
CONFIG 标签: color #4caf5088, fontWeight bold, letterSpacing 0.05em
条目 label: color #3a5a3a
条目 value: color #6a8a6a, fontFamily monospace
路径: color #2a3a2a, fontSize 10px
```

### 5.10 滚动条（WebKit）

```css
::-webkit-scrollbar { width: 5px; }
::-webkit-scrollbar-track { background: #0d0d0d; }
::-webkit-scrollbar-thumb { background: #2a2a2a; border-radius: 3px; }
::-webkit-scrollbar-thumb:hover { background: #3a3a3a; }
```

---

## 6. 布局结构

全局采用 `height: 100vh` flex column 布局：

```
┌─────────────────────────────────┐
│  标题栏（固定高，拖拽区）        │  background #080808
├─────────────────────────────────┤
│  配置横幅（按需显示）            │  background #0b0f0b
├─────────────────────────────────┤
│  过滤栏（固定高）                │  background #0a0a0a
├─────────────────────────────────┤
│                                 │
│  日志滚动区（flex: 1, 溢出滚动）│  background #0d0d0d
│                                 │
├─────────────────────────────────┤
│  状态栏（固定高）                │  background #0a0a0a
└─────────────────────────────────┘
```

- 所有固定高区域设置 `flexShrink: 0`
- 日志区设置 `flex: 1, overflowY: auto`
- 模态弹窗叠加在布局上方，通过 `position: fixed` 实现

---

## 7. 动效规范

仅使用微量过渡，不做大幅动画：

| 元素 | 效果 |
|------|------|
| 按钮 hover/active | `transition: opacity 0.15s` |
| 切换开关 | `transition: all 0.2s` |
| 复制按钮颜色变化 | `transition: color 0.2s`，1.2s 后自动恢复 |
| 日志自动滚动 | `scrollIntoView({ behavior: 'smooth' })` |

禁止使用 CSS 动画、keyframe、`transform` 过渡等装饰性动效。

---

## 8. 数据库连接名颜色生成

多连接场景下，连接名标签的颜色由名称哈希决定，保证颜色稳定：

```js
const CONN_COLORS = ['#2196f3', '#4caf50', '#ff9800', '#9c27b0', '#00bcd4'];
function connColor(connName) {
  let n = 0;
  for (let i = 0; i < connName.length; i++) n += connName.charCodeAt(i);
  return CONN_COLORS[n % CONN_COLORS.length];
}
```

---

## 9. 禁止事项

- 禁止引入任何 CSS 框架（Tailwind、Bootstrap 等）
- 禁止使用 `font-size > 13px`（除非有特殊说明）
- 禁止使用彩色渐变背景或装饰性图案
- 禁止随意引入新的颜色，必须从现有色彩体系中取值
- 禁止使用 `box-shadow` 作装饰（仅在必要时用极暗投影）
- 禁止 Class 组件，全用函数式组件 + Hooks
- 禁止在前端使用 fetch / axios，只走 Wails IPC
