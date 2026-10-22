export namespace adb {
	
	export class ProcConnection {
	    local: string;
	    remote: string;
	    state: string;
	    pid: number;
	    proc: string;
	
	    static createFrom(source: any = {}) {
	        return new ProcConnection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.local = source["local"];
	        this.remote = source["remote"];
	        this.state = source["state"];
	        this.pid = source["pid"];
	        this.proc = source["proc"];
	    }
	}

}

export namespace mumu {
	
	export class EmuInstance {
	    index: number;
	    name: string;
	    serial: string;
	    running: boolean;
	    android_started: boolean;
	    android_version: string;
	    pid: number;
	
	    static createFrom(source: any = {}) {
	        return new EmuInstance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.name = source["name"];
	        this.serial = source["serial"];
	        this.running = source["running"];
	        this.android_started = source["android_started"];
	        this.android_version = source["android_version"];
	        this.pid = source["pid"];
	    }
	}
	export class InstallInfo {
	    installed: boolean;
	    dir: string;
	    manager_path: string;
	    adb_path: string;
	
	    static createFrom(source: any = {}) {
	        return new InstallInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = source["installed"];
	        this.dir = source["dir"];
	        this.manager_path = source["manager_path"];
	        this.adb_path = source["adb_path"];
	    }
	}

}

