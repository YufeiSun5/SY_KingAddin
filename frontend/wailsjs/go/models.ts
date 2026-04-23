export namespace db {
	
	export class BatchConfig {
	    queue_size: number;
	    flush_interval: number;
	    flush_batch: number;
	    max_retries: number;
	    dead_mem_limit: number;
	    conn_retry_base_interval: number;
	    conn_retry_max_interval: number;
	    retry_state_persist_interval: number;
	
	    static createFrom(source: any = {}) {
	        return new BatchConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.queue_size = source["queue_size"];
	        this.flush_interval = source["flush_interval"];
	        this.flush_batch = source["flush_batch"];
	        this.max_retries = source["max_retries"];
	        this.dead_mem_limit = source["dead_mem_limit"];
	        this.conn_retry_base_interval = source["conn_retry_base_interval"];
	        this.conn_retry_max_interval = source["conn_retry_max_interval"];
	        this.retry_state_persist_interval = source["retry_state_persist_interval"];
	    }
	}
	export class ConnConfig {
	    name: string;
	    type: string;
	    host: string;
	    port: number;
	    dbname: string;
	    user: string;
	    password: string;
	    dsn: string;
	    is_kh: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConnConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.dbname = source["dbname"];
	        this.user = source["user"];
	        this.password = source["password"];
	        this.dsn = source["dsn"];
	        this.is_kh = source["is_kh"];
	    }
	}
	export class ConnStatus {
	    name: string;
	    type: string;
	    is_connected: boolean;
	    display: string;
	    is_kh: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConnStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.is_connected = source["is_connected"];
	        this.display = source["display"];
	        this.is_kh = source["is_kh"];
	    }
	}

}

export namespace logger {
	
	export class Entry {
	    time: string;
	    level: string;
	    source: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.source = source["source"];
	        this.message = source["message"];
	    }
	}

}

export namespace main {
	
	export class APIConfig {
	    host: string;
	    port: number;
	    my_ip: string;
	
	    static createFrom(source: any = {}) {
	        return new APIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.port = source["port"];
	        this.my_ip = source["my_ip"];
	    }
	}
	export class ScadaConfig {
	    base_url: string;
	    username: string;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new ScadaConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.base_url = source["base_url"];
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}
	export class MysqlConfig {
	    host: string;
	    port: number;
	    dbname: string;
	    user: string;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new MysqlConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.port = source["port"];
	        this.dbname = source["dbname"];
	        this.user = source["user"];
	        this.password = source["password"];
	    }
	}
	export class AppConfig {
	    mysql: MysqlConfig;
	    databases: db.ConnConfig[];
	    api: APIConfig;
	    scada: ScadaConfig;
	    batch: db.BatchConfig;
	
	    static createFrom(source: any = {}) {
	        return new AppConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mysql = this.convertValues(source["mysql"], MysqlConfig);
	        this.databases = this.convertValues(source["databases"], db.ConnConfig);
	        this.api = this.convertValues(source["api"], APIConfig);
	        this.scada = this.convertValues(source["scada"], ScadaConfig);
	        this.batch = this.convertValues(source["batch"], db.BatchConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

export namespace scada {
	
	export class TokenStatus {
	    has_token: boolean;
	    is_expired: boolean;
	    should_refresh: boolean;
	    elapsed_seconds?: number;
	    remaining_seconds?: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.has_token = source["has_token"];
	        this.is_expired = source["is_expired"];
	        this.should_refresh = source["should_refresh"];
	        this.elapsed_seconds = source["elapsed_seconds"];
	        this.remaining_seconds = source["remaining_seconds"];
	    }
	}
	export class ConnectionStatus {
	    is_connected: boolean;
	    has_token: boolean;
	    token_preview?: string;
	    token_status: TokenStatus;
	    token_acquired_at?: string;
	    token_expires_at?: string;
	    token_refresh_at?: string;
	    cooldown_seconds: number;
	    reconnect_available: boolean;
	    cooldown_remaining: number;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.is_connected = source["is_connected"];
	        this.has_token = source["has_token"];
	        this.token_preview = source["token_preview"];
	        this.token_status = this.convertValues(source["token_status"], TokenStatus);
	        this.token_acquired_at = source["token_acquired_at"];
	        this.token_expires_at = source["token_expires_at"];
	        this.token_refresh_at = source["token_refresh_at"];
	        this.cooldown_seconds = source["cooldown_seconds"];
	        this.reconnect_available = source["reconnect_available"];
	        this.cooldown_remaining = source["cooldown_remaining"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

