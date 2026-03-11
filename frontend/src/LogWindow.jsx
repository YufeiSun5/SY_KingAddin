import { useState, useEffect, useRef, useCallback } from 'react';
import {
  GetLogHistory, GetScadaStatus, GetDBConnectionsList, ReconnectDB,
  LoadConfig, ApplyConfig, GetAutoStart, SetAutoStart, GetConfigPath,
} from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

// ── 颜色配置 ────────────────────────────────────────────────────────────────

const SOURCE_BG = {
  scada: '#0d1a0d',
  menu:  '#0d0d1a',
  http:  '#1a0d1a',
  app:   '#1a1a0d',
  db:    '#1a0d0d',
};

const LEVEL_COLOR = {
  info:  '#e0e0e0',
  warn:  '#f5c518',
  error: '#ff4444',
};

const SOURCE_TAG_COLOR = {
  scada: '#4caf50',
  menu:  '#2196f3',
  http:  '#9c27b0',
  app:   '#ff9800',
  db:    '#f44336',
};

// ── 通用输入框样式 ───────────────────────────────────────────────────────────

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

// 从 db 类日志消息中解析连接名（如 "[主库]"）
function parseDbConnFromMessage(message) {
  if (typeof message !== 'string') return null;
  const m = message.match(/\[([^\]]+)\]/);
  return m ? m[1] : null;
}

// 按连接名生成稳定颜色（用于数据源分类显示）
const CONN_COLORS = ['#2196f3', '#4caf50', '#ff9800', '#9c27b0', '#00bcd4'];
function connColor(connName) {
  let n = 0;
  for (let i = 0; i < connName.length; i++) n += connName.charCodeAt(i);
  return CONN_COLORS[n % CONN_COLORS.length];
}

// ── 单条日志行 ───────────────────────────────────────────────────────────────

function LogLine({ entry, index }) {
  const bg = SOURCE_BG[entry.source] || '#111';
  const bgAlt = bg.replace(/#(\w{2})(\w{2})(\w{2})/, (_, r, g, b) => {
    const dim = (hex) => Math.max(0, parseInt(hex, 16) - 6).toString(16).padStart(2, '0');
    return `#${dim(r)}${dim(g)}${dim(b)}`;
  });
  const fg = LEVEL_COLOR[entry.level] || '#e0e0e0';
  const tagColor = SOURCE_TAG_COLOR[entry.source] || '#888';
  const dbConn = entry.source === 'db' ? parseDbConnFromMessage(entry.message) : null;

  return (
    <div style={{
      background: index % 2 === 0 ? bg : bgAlt,
      padding: '2px 10px',
      fontFamily: '"Consolas", "JetBrains Mono", monospace',
      fontSize: '12.5px',
      lineHeight: '1.6',
      borderLeft: `2px solid ${dbConn ? connColor(dbConn) + '99' : tagColor + '22'}`,
      display: 'flex',
      gap: '8px',
      alignItems: 'baseline',
    }}>
      <span style={{ color: '#555', flexShrink: 0, fontSize: '11px' }}>{entry.time}</span>
      <span style={{
        color: tagColor, flexShrink: 0, fontSize: '10px', fontWeight: 'bold',
        letterSpacing: '0.05em', minWidth: '38px', textAlign: 'right',
      }}>
        {entry.source.toUpperCase()}
      </span>
      {dbConn && (
        <span style={{
          flexShrink: 0, fontSize: '10px', color: connColor(dbConn), fontWeight: 'bold',
          padding: '0 4px', borderRadius: '3px', background: connColor(dbConn) + '22',
        }}>
          {dbConn}
        </span>
      )}
      <span style={{ color: fg, wordBreak: 'break-all' }}>{entry.message}</span>
    </div>
  );
}

// ── 配置信息横幅 ─────────────────────────────────────────────────────────────

function ConfigBanner({ config, configPath, dbConnections }) {
  if (!config) return null;
  const dbSummary = (dbConnections && dbConnections.length > 0)
    ? dbConnections.map(c => `${c.name}(${c.display})`).join(' · ')
    : `${config.mysql?.host}:${config.mysql?.port}/${config.mysql?.dbname}`;
  const items = [
    { label: '数据库', value: dbSummary },
    { label: 'API',   value: `${config.api?.my_ip}:${config.api?.port}` },
    { label: 'SCADA', value: config.scada?.base_url },
  ];
  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      gap: '16px',
      padding: '4px 12px',
      background: '#0b0f0b',
      borderBottom: '1px solid #1a2a1a',
      fontSize: '11px',
      color: '#555',
      flexShrink: 0,
      flexWrap: 'wrap',
    }}>
      <span style={{ color: '#4caf5088', fontWeight: 'bold', letterSpacing: '0.05em' }}>
        CONFIG
      </span>
      {items.map(({ label, value }) => (
        <span key={label} style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
          <span style={{ color: '#3a5a3a' }}>{label}:</span>
          <span style={{ color: '#6a8a6a', fontFamily: 'monospace' }}>{value}</span>
        </span>
      ))}
      {configPath && (
        <>
          <span style={{ color: '#1a2a1a' }}>|</span>
          <span style={{ color: '#2a3a2a', fontFamily: 'monospace', fontSize: '10px' }}>
            {configPath}
          </span>
        </>
      )}
    </div>
  );
}

// ── 设置弹窗 ─────────────────────────────────────────────────────────────────

function Field({ label, value, onChange, type = 'text' }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '3px' }}>
      <label style={{ color: '#555', fontSize: '11px' }}>{label}</label>
      <input
        type={type}
        value={value ?? ''}
        onChange={e => onChange(type === 'number' ? Number(e.target.value) : e.target.value)}
        style={inputStyle}
      />
    </div>
  );
}

function Section({ title, color, children }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
      <div style={{
        fontSize: '11px', fontWeight: 'bold', color, letterSpacing: '0.08em',
        borderBottom: `1px solid ${color}33`, paddingBottom: '4px',
      }}>
        {title}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
        {children}
      </div>
    </div>
  );
}

// 从 config 得到可编辑的数据库连接列表（无 databases 时用 mysql 生成一条）
function getEditableDbList(cfg) {
  if (!cfg) return [];
  if (cfg.databases && cfg.databases.length > 0) return cfg.databases;
  const m = cfg.mysql || {};
  return [{
    name: 'default',
    type: 'mysql',
    host: m.host ?? '127.0.0.1',
    port: m.port ?? 3306,
    dbname: m.dbname ?? 'sy',
    user: m.user ?? 'root',
    password: m.password ?? '',
    dsn: '',
    is_kh: false,
  }];
}

function DbConnEditor({ conn, onChange, onRemove, canRemove }) {
  const isOdbc = conn.type === 'odbc';
  const update = (key, val) => onChange({ ...conn, [key]: val });
  return (
    <div style={{
      background: '#0d0d12', border: '1px solid #252530', borderRadius: '6px',
      padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: '8px',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
        <input
          placeholder="连接名称"
          value={conn.name ?? ''}
          onChange={e => update('name', e.target.value)}
          style={{ ...inputStyle, width: '120px' }}
        />
        <select
          value={conn.type ?? 'mysql'}
          onChange={e => update('type', e.target.value)}
          style={{ ...inputStyle, width: '80px' }}
        >
          <option value="mysql">MySQL</option>
          <option value="odbc">ODBC</option>
        </select>
        {canRemove && (
          <button
            type="button"
            onClick={onRemove}
            style={{
              padding: '2px 8px', fontSize: '11px', background: '#2a1a1a', border: '1px solid #443', color: '#f44336',
              borderRadius: '4px', cursor: 'pointer',
            }}
          >
            删除
          </button>
        )}
      </div>
      {isOdbc ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
            <Field label="DSN 数据源名" value={conn.dsn} onChange={v => update('dsn', v)} />
            <Field label="用户名" value={conn.user} onChange={v => update('user', v)} />
            <div style={{ gridColumn: '1 / -1' }}>
              <Field label="密码" value={conn.password} type="password" onChange={v => update('password', v)} />
            </div>
          </div>
          {/* KH 工业库模式开关 */}
          <div
            onClick={() => update('is_kh', !conn.is_kh)}
            style={{
              display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer',
              padding: '6px 8px', borderRadius: '4px',
              background: conn.is_kh ? '#1a1a0d' : '#111',
              border: `1px solid ${conn.is_kh ? '#ff9800' : '#252530'}`,
              userSelect: 'none',
            }}
          >
            <div style={{
              width: '14px', height: '14px', borderRadius: '3px', flexShrink: 0,
              background: conn.is_kh ? '#ff9800' : 'transparent',
              border: `2px solid ${conn.is_kh ? '#ff9800' : '#555'}`,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}>
              {conn.is_kh && <span style={{ color: '#000', fontSize: '10px', fontWeight: 'bold', lineHeight: 1 }}>✓</span>}
            </div>
            <span style={{ fontSize: '11px', color: conn.is_kh ? '#ff9800' : '#666' }}>
              KH 工业库模式
            </span>
            <span style={{ fontSize: '10px', color: '#444', marginLeft: '4px' }}>
              （整段 SQL 一次发送，支持 SET + SELECT 多语句）
            </span>
          </div>
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
          <Field label="主机" value={conn.host} onChange={v => update('host', v)} />
          <Field label="端口" value={conn.port} type="number" onChange={v => update('port', v)} />
          <Field label="数据库" value={conn.dbname} onChange={v => update('dbname', v)} />
          <Field label="用户名" value={conn.user} onChange={v => update('user', v)} />
          <div style={{ gridColumn: '1 / -1' }}>
            <Field label="密码" value={conn.password} type="password" onChange={v => update('password', v)} />
          </div>
        </div>
      )}
    </div>
  );
}

function SettingsModal({ onClose, onSaved, dbConnections }) {
  const [cfg, setCfg] = useState(null);
  const [dbList, setDbList] = useState([]); // 可编辑的数据库连接列表
  const [autoStart, setAutoStart] = useState(false);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');

  useEffect(() => {
    LoadConfig().then(c => {
      setCfg(c);
      setDbList(getEditableDbList(c));
    }).catch(() => {});
    GetAutoStart().then(v => setAutoStart(v)).catch(() => {});
  }, []);

  const set = (section, key, val) =>
    setCfg(prev => ({ ...prev, [section]: { ...prev[section], [key]: val } }));

  const setDbAt = (index, conn) => {
    setDbList(prev => {
      const next = [...prev];
      next[index] = conn;
      return next;
    });
  };
  const addDb = () => {
    setDbList(prev => [...prev, {
      name: `连接${prev.length + 1}`,
      type: 'mysql',
      host: '127.0.0.1',
      port: 3306,
      dbname: 'sy',
      user: 'root',
      password: '',
      dsn: '',
      is_kh: false,
    }]);
  };
  const removeDb = (index) => {
    setDbList(prev => prev.filter((_, i) => i !== index));
  };

  const handleSave = async () => {
    setSaving(true);
    setMsg('');
    try {
      const toSave = { ...cfg };
      toSave.databases = dbList.length > 0 ? dbList : [];
      const firstMysql = dbList.find(c => c.type === 'mysql');
      if (firstMysql) {
        toSave.mysql = {
          host: firstMysql.host ?? '127.0.0.1',
          port: firstMysql.port ?? 3306,
          dbname: firstMysql.dbname ?? 'sy',
          user: firstMysql.user ?? 'root',
          password: firstMysql.password ?? '',
        };
      }
      await ApplyConfig(toSave);
      await SetAutoStart(autoStart).then(() => true).catch(e => { throw e; });
      setMsg('✅ 已保存并热重载');
      onSaved && onSaved(toSave);
    } catch (e) {
      setMsg('❌ 保存失败: ' + String(e));
    } finally {
      setSaving(false);
    }
  };

  const overlay = {
    position: 'fixed', inset: 0,
    background: 'rgba(0,0,0,0.75)',
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    zIndex: 1000,
  };

  const modal = {
    background: '#0f0f0f',
    border: '1px solid #222',
    borderRadius: '8px',
    padding: '20px 24px',
    width: '520px',
    maxHeight: '90vh',
    overflowY: 'auto',
    display: 'flex',
    flexDirection: 'column',
    gap: '16px',
    scrollbarWidth: 'thin',
    scrollbarColor: '#2a2a2a #0f0f0f',
  };

  const btnBase = {
    borderRadius: '4px',
    padding: '5px 16px',
    fontSize: '12px',
    cursor: 'pointer',
    border: '1px solid',
  };

  if (!cfg) {
    return (
      <div style={overlay}>
        <div style={{ ...modal, alignItems: 'center', color: '#444' }}>加载配置中...</div>
      </div>
    );
  }

  return (
    <div style={overlay} onClick={e => e.target === e.currentTarget && onClose()}>
      <div style={modal}>
        {/* 标题 */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <span style={{ color: '#e0e0e0', fontWeight: 'bold', fontSize: '13px' }}>
            ⚙ 设置
          </span>
          <button
            onClick={onClose}
            style={{ ...btnBase, background: 'transparent', border: '1px solid #333', color: '#555' }}>
            关闭
          </button>
        </div>

        {/* 数据库连接（可添加/编辑/删除） */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: '11px', fontWeight: 'bold', color: '#2196f3', letterSpacing: '0.08em', borderBottom: '1px solid #2196f333', paddingBottom: '4px' }}>
              数据库连接
            </span>
            <button
              type="button"
              onClick={addDb}
              style={{
                padding: '4px 10px', fontSize: '11px', background: '#1a2a3a', border: '1px solid #2196f3', color: '#90caf9',
                borderRadius: '4px', cursor: 'pointer',
              }}
            >
              + 添加连接
            </button>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
            {dbList.map((conn, idx) => (
              <DbConnEditor
                key={idx}
                conn={conn}
                onChange={c => setDbAt(idx, c)}
                onRemove={() => removeDb(idx)}
                canRemove={dbList.length > 1}
              />
            ))}
          </div>
          {dbConnections && dbConnections.length > 0 && (
            <div style={{ fontSize: '10px', color: '#555' }}>
              当前运行中：{dbConnections.map(c => (
                <span key={c.name} style={{ marginRight: '8px' }}>
                  <span style={{ width: '5px', height: '5px', borderRadius: '50%', background: c.is_connected ? '#2196f3' : '#555', display: 'inline-block', marginRight: '4px' }} />
                  {c.name}
                </span>
              ))}
            </div>
          )}
        </div>

        {/* API */}
        <Section title="API 服务" color="#9c27b0">
          <Field label="监听 IP" value={cfg.api?.host} onChange={v => set('api', 'host', v)} />
          <Field label="端口" value={cfg.api?.port} type="number" onChange={v => set('api', 'port', v)} />
          <div style={{ gridColumn: '1 / -1' }}>
            <Field label="对外 IP（SCADA 回调）" value={cfg.api?.my_ip} onChange={v => set('api', 'my_ip', v)} />
          </div>
        </Section>

        {/* SCADA */}
        <Section title="SCADA" color="#4caf50">
          <div style={{ gridColumn: '1 / -1' }}>
            <Field label="接口地址" value={cfg.scada?.base_url} onChange={v => set('scada', 'base_url', v)} />
          </div>
          <Field label="用户名" value={cfg.scada?.username} onChange={v => set('scada', 'username', v)} />
          <Field label="密码（MD5）" value={cfg.scada?.password} onChange={v => set('scada', 'password', v)} />
        </Section>

        {/* 开机自启 */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: '10px',
          padding: '8px 10px',
          background: '#111', border: '1px solid #1e1e1e', borderRadius: '4px',
        }}>
          <span style={{ color: '#888', fontSize: '12px', flex: 1 }}>开机自动启动</span>
          <div
            onClick={() => setAutoStart(v => !v)}
            style={{
              width: '36px', height: '20px', borderRadius: '10px',
              background: autoStart ? '#1a5c1a' : '#1a1a1a',
              border: `1px solid ${autoStart ? '#4caf50' : '#333'}`,
              position: 'relative', cursor: 'pointer', transition: 'all 0.2s',
            }}>
            <div style={{
              position: 'absolute', top: '2px',
              left: autoStart ? '17px' : '2px',
              width: '14px', height: '14px', borderRadius: '50%',
              background: autoStart ? '#4caf50' : '#444',
              transition: 'all 0.2s',
            }} />
          </div>
          <span style={{ color: autoStart ? '#4caf50' : '#444', fontSize: '11px', minWidth: '28px' }}>
            {autoStart ? '开启' : '关闭'}
          </span>
        </div>

        {/* 操作 */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '4px' }}>
          {msg && <span style={{ fontSize: '11px', color: msg.startsWith('✅') ? '#4caf50' : '#f44336', flex: 1 }}>{msg}</span>}
          {!msg && <span style={{ flex: 1 }} />}
          <button
            onClick={onClose}
            style={{ ...btnBase, background: '#111', borderColor: '#333', color: '#666' }}>
            取消
          </button>
          <button
            onClick={handleSave}
            disabled={saving}
            style={{
              ...btnBase,
              background: saving ? '#0d2a0d' : '#1a3a1a',
              borderColor: '#4caf50',
              color: saving ? '#4caf5088' : '#4caf50',
            }}>
            {saving ? '保存中...' : '保存并热重载'}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── 状态栏 ───────────────────────────────────────────────────────────────────

function StatusBar({ count, connected, tokenPreview, dbConnections, autoScroll, onToggleScroll, onClear, onOpenSettings, onReconnectDB }) {
  const dot = (ok, okColor = '#4caf50', noColor = '#f44336') => ({
    width: '7px', height: '7px', borderRadius: '50%',
    background: ok ? okColor : noColor,
    display: 'inline-block',
  });

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '16px',
      padding: '4px 12px',
      background: '#0a0a0a', borderTop: '1px solid #222',
      fontSize: '11px', color: '#555', flexShrink: 0,
      flexWrap: 'wrap',
    }}>
      <span style={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
        <span style={dot(connected)} />
        <span style={{ color: connected ? '#4caf50' : '#f44336' }}>
          {connected ? 'SCADA 已连接' : 'SCADA 未连接'}
        </span>
        {tokenPreview && <span style={{ color: '#444' }}>· Token: {tokenPreview}</span>}
      </span>

      <span style={{ color: '#333' }}>|</span>

      <span style={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' }}>
        {(dbConnections && dbConnections.length > 0) ? dbConnections.map(c => (
          <span key={c.name} style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <span style={dot(c.is_connected, '#2196f3', '#555')} />
            <span style={{ color: c.is_connected ? '#2196f3' : '#555' }}>{c.name}</span>
            <button
              onClick={() => onReconnectDB(c.name)}
              style={{
                marginLeft: '2px', padding: '1px 6px', fontSize: '10px',
                background: '#1a1a2a', border: '1px solid #333', color: '#90caf9',
                borderRadius: '3px', cursor: 'pointer',
              }}
              title="断线重连"
            >
              重连
            </button>
          </span>
        )) : (
          <>
            <span style={dot(false, '#2196f3', '#555')} />
            <span style={{ color: '#555' }}>数据库 无连接</span>
          </>
        )}
      </span>

      <span style={{ color: '#333' }}>|</span>
      <span>{count} 条日志</span>
      <span style={{ flex: 1 }} />

      <button onClick={onToggleScroll} style={{
        background: autoScroll ? '#1a3a1a' : '#1a1a1a',
        border: `1px solid ${autoScroll ? '#4caf50' : '#333'}`,
        color: autoScroll ? '#4caf50' : '#555',
        borderRadius: '3px', padding: '2px 8px', cursor: 'pointer', fontSize: '11px',
      }}>
        {autoScroll ? '↓ 跟随' : '↓ 已暂停'}
      </button>

      <button onClick={onClear} style={{
        background: '#1a1a1a', border: '1px solid #333', color: '#555',
        borderRadius: '3px', padding: '2px 8px', cursor: 'pointer', fontSize: '11px',
      }}>
        清空
      </button>

      <button onClick={onOpenSettings} style={{
        background: '#111', border: '1px solid #2a2a2a', color: '#666',
        borderRadius: '3px', padding: '2px 8px', cursor: 'pointer', fontSize: '11px',
      }}>
        ⚙ 设置
      </button>
    </div>
  );
}

// ── 过滤栏 ───────────────────────────────────────────────────────────────────

const LEVELS = ['all', 'info', 'warn', 'error'];
const SOURCES = ['all', 'scada', 'menu', 'http', 'app', 'db'];

function FilterBar({ level, source, keyword, dataSource, dbConnections, onLevel, onSource, onKeyword, onDataSource }) {
  const btnStyle = (active) => ({
    background: active ? '#1e3a5f' : '#111',
    border: `1px solid ${active ? '#2196f3' : '#222'}`,
    color: active ? '#90caf9' : '#555',
    borderRadius: '3px', padding: '2px 10px', cursor: 'pointer', fontSize: '11px',
  });
  const dsBtnStyle = (active) => ({
    background: active ? '#1a2a1a' : '#111',
    border: `1px solid ${active ? '#2196f3' : '#222'}`,
    color: active ? '#90caf9' : '#555',
    borderRadius: '3px', padding: '2px 8px', cursor: 'pointer', fontSize: '11px',
  });

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '8px',
      padding: '6px 12px',
      background: '#0a0a0a', borderBottom: '1px solid #1a1a1a',
      flexShrink: 0, flexWrap: 'wrap',
    }}>
      <span style={{ color: '#444', fontSize: '11px' }}>级别:</span>
      {LEVELS.map(l => (
        <button key={l} style={btnStyle(level === l)} onClick={() => onLevel(l)}>
          {l.toUpperCase()}
        </button>
      ))}
      <span style={{ color: '#222' }}>|</span>
      <span style={{ color: '#444', fontSize: '11px' }}>来源:</span>
      {SOURCES.map(s => (
        <button key={s} style={btnStyle(source === s)} onClick={() => onSource(s)}>
          {s.toUpperCase()}
        </button>
      ))}
      {source === 'db' && dbConnections && dbConnections.length > 0 && (
        <>
          <span style={{ color: '#222' }}>|</span>
          <span style={{ color: '#444', fontSize: '11px' }}>数据源:</span>
          <button style={dsBtnStyle(dataSource === '')} onClick={() => onDataSource('')}>全部</button>
          {dbConnections.map(c => (
            <button key={c.name} style={dsBtnStyle(dataSource === c.name)} onClick={() => onDataSource(c.name)}>
              {c.name}
            </button>
          ))}
        </>
      )}
      <span style={{ color: '#222' }}>|</span>
      <input
        value={keyword}
        onChange={e => onKeyword(e.target.value)}
        placeholder="搜索..."
        style={{
          background: '#111', border: '1px solid #222', color: '#888',
          borderRadius: '3px', padding: '2px 8px', fontSize: '11px',
          outline: 'none', width: '160px',
        }}
      />
    </div>
  );
}

// ── 主组件 ───────────────────────────────────────────────────────────────────

export default function LogWindow() {
  const [entries, setEntries] = useState([]);
  const [autoScroll, setAutoScroll] = useState(true);
  const [filterLevel, setFilterLevel] = useState('all');
  const [filterSource, setFilterSource] = useState('all');
  const [filterDataSource, setFilterDataSource] = useState(''); // 数据源：仅当来源为 db 时按连接名筛选
  const [keyword, setKeyword] = useState('');
  const [scadaStatus, setScadaStatus] = useState({ is_connected: false });
  const [dbConnections, setDbConnections] = useState([]);
  const [config, setConfig] = useState(null);
  const [configPath, setConfigPath] = useState('');
  const [showSettings, setShowSettings] = useState(false);

  const bottomRef = useRef(null);
  const containerRef = useRef(null);

  // 初始化：拉历史日志 + 配置 + 状态
  useEffect(() => {
    GetLogHistory().then(hist => { if (hist?.length > 0) setEntries(hist); }).catch(() => {});

    LoadConfig().then(setConfig).catch(() => {});
    GetConfigPath().then(setConfigPath).catch(() => {});

    const refreshStatus = () => {
      GetScadaStatus().then(s => setScadaStatus(s)).catch(() => {});
      GetDBConnectionsList().then(list => setDbConnections(list || [])).catch(() => {});
    };
    refreshStatus();
    const tid = setInterval(refreshStatus, 5000);
    return () => clearInterval(tid);
  }, []);

  // 监听后端配置加载成功事件（启动时 / 热重载后）
  useEffect(() => {
    const off = EventsOn('config:loaded', (cfg) => {
      setConfig(cfg);
    });
    return off;
  }, []);

  // 监听实时日志
  useEffect(() => {
    const off = EventsOn('log:entry', (entry) => {
      setEntries(prev => {
        const next = [...prev, entry];
        return next.length > 2000 ? next.slice(-2000) : next;
      });
    });
    return off;
  }, []);

  // 自动滚动
  useEffect(() => {
    if (autoScroll && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [entries, autoScroll]);

  const handleScroll = useCallback(() => {
    if (!containerRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = containerRef.current;
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 60);
  }, []);

  const filtered = entries.filter(e => {
    if (filterLevel !== 'all' && e.level !== filterLevel) return false;
    if (filterSource !== 'all' && e.source !== filterSource) return false;
    if (filterSource === 'db' && filterDataSource && e.source === 'db') {
      const conn = parseDbConnFromMessage(e.message);
      if (conn !== filterDataSource) return false;
    }
    if (keyword && !e.message.toLowerCase().includes(keyword.toLowerCase())) return false;
    return true;
  });

  return (
    <div style={{
      display: 'flex', flexDirection: 'column', height: '100vh',
      background: '#0d0d0d', color: '#ccc', overflow: 'hidden',
    }}>
      {/* 标题栏 */}
      <div style={{
        padding: '8px 14px', background: '#080808', borderBottom: '1px solid #1a1a1a',
        display: 'flex', alignItems: 'center', gap: '10px', flexShrink: 0,
        '--wails-draggable': 'drag', userSelect: 'none',
      }}>
        <span style={{ fontSize: '13px', fontWeight: 'bold', color: '#e0e0e0', letterSpacing: '0.05em' }}>
          GOKS 插件服务
        </span>
        <span style={{ fontSize: '11px', color: '#444' }}>后端事件日志</span>
      </div>

      {/* 配置信息横幅 */}
      <ConfigBanner config={config} configPath={configPath} dbConnections={dbConnections} />

      {/* 过滤栏 */}
      <FilterBar
        level={filterLevel} source={filterSource} keyword={keyword}
        dataSource={filterDataSource} dbConnections={dbConnections}
        onLevel={setFilterLevel} onSource={setFilterSource} onKeyword={setKeyword}
        onDataSource={setFilterDataSource}
      />

      {/* 日志区域 */}
      <div
        ref={containerRef}
        onScroll={handleScroll}
        style={{
          flex: 1, overflowY: 'auto', overflowX: 'hidden', padding: '4px 0',
          scrollbarWidth: 'thin', scrollbarColor: '#2a2a2a #0d0d0d',
        }}
      >
        {filtered.length === 0 ? (
          <div style={{ color: '#333', textAlign: 'center', marginTop: '80px', fontSize: '13px' }}>
            暂无日志
          </div>
        ) : (
          filtered.map((e, i) => <LogLine key={i} entry={e} index={i} />)
        )}
        <div ref={bottomRef} />
      </div>

      {/* 状态栏 */}
      <StatusBar
        count={filtered.length}
        connected={scadaStatus.is_connected}
        tokenPreview={scadaStatus.token_preview}
        dbConnections={dbConnections}
        autoScroll={autoScroll}
        onToggleScroll={() => setAutoScroll(v => !v)}
        onClear={() => setEntries([])}
        onOpenSettings={() => setShowSettings(true)}
        onReconnectDB={async (name) => {
          try {
            await ReconnectDB(name);
            GetDBConnectionsList().then(list => setDbConnections(list || [])).catch(() => {});
          } catch (e) {
            console.error('重连失败', e);
          }
        }}
      />

      {/* 设置弹窗 */}
      {showSettings && (
        <SettingsModal
          onClose={() => setShowSettings(false)}
          onSaved={(newCfg) => {
            setConfig(newCfg);
            setShowSettings(false);
          }}
          dbConnections={dbConnections}
        />
      )}
    </div>
  );
}
