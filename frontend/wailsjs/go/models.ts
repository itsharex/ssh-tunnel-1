export namespace config {
	
	export class Forward {
	    type: string;
	    listen: string;
	    target: string;
	
	    static createFrom(source: any = {}) {
	        return new Forward(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.listen = source["listen"];
	        this.target = source["target"];
	    }
	}
	export class Proxy {
	    type?: string;
	    host?: string;
	    port?: number;
	    user?: string;
	    password?: string;
	
	    static createFrom(source: any = {}) {
	        return new Proxy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.password = source["password"];
	    }
	}
	export class Settings {
	    autostart: boolean;
	    theme: string;
	    minimizeToTray: boolean;
	    startMinimized: boolean;
	    hideFromDock: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.autostart = source["autostart"];
	        this.theme = source["theme"];
	        this.minimizeToTray = source["minimizeToTray"];
	        this.startMinimized = source["startMinimized"];
	        this.hideFromDock = source["hideFromDock"];
	    }
	}
	export class Tunnel {
	    id: string;
	    name: string;
	    host: string;
	    port: number;
	    user: string;
	    authType: string;
	    password?: string;
	    keyPath?: string;
	    keyPassphrase?: string;
	    proxy?: Proxy;
	    forwards: Forward[];
	    autoReconnect: boolean;
	    reconnectMinMs: number;
	    reconnectMaxMs: number;
	    serverAliveInterval: number;
	    status?: string;
	    lastError?: string;
	    startedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Tunnel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.authType = source["authType"];
	        this.password = source["password"];
	        this.keyPath = source["keyPath"];
	        this.keyPassphrase = source["keyPassphrase"];
	        this.proxy = this.convertValues(source["proxy"], Proxy);
	        this.forwards = this.convertValues(source["forwards"], Forward);
	        this.autoReconnect = source["autoReconnect"];
	        this.reconnectMinMs = source["reconnectMinMs"];
	        this.reconnectMaxMs = source["reconnectMaxMs"];
	        this.serverAliveInterval = source["serverAliveInterval"];
	        this.status = source["status"];
	        this.lastError = source["lastError"];
	        this.startedAt = source["startedAt"];
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

export namespace conn {
	
	export class Snapshot {
	    id: string;
	    tunnelId: string;
	    tunnel: string;
	    forward: string;
	    proto: string;
	    src: string;
	    dst: string;
	    pid: number;
	    proc: string;
	    state: string;
	    reason: string;
	    up: number;
	    down: number;
	    upRate: number;
	    downRate: number;
	    started: number;
	    closedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.tunnelId = source["tunnelId"];
	        this.tunnel = source["tunnel"];
	        this.forward = source["forward"];
	        this.proto = source["proto"];
	        this.src = source["src"];
	        this.dst = source["dst"];
	        this.pid = source["pid"];
	        this.proc = source["proc"];
	        this.state = source["state"];
	        this.reason = source["reason"];
	        this.up = source["up"];
	        this.down = source["down"];
	        this.upRate = source["upRate"];
	        this.downRate = source["downRate"];
	        this.started = source["started"];
	        this.closedAt = source["closedAt"];
	    }
	}

}

export namespace main {
	
	export class AppInfo {
	    version: string;
	    author: string;
	    email: string;
	    githubUrl: string;
	    license: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.author = source["author"];
	        this.email = source["email"];
	        this.githubUrl = source["githubUrl"];
	        this.license = source["license"];
	    }
	}

}

