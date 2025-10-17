export namespace main {
	
	export class IncrementalBackupConfig {
	    enableBackup: boolean;
	    backupPath: string;
	    lastBackupTime: number;
	    maxBackupVersions: number;
	
	    static createFrom(source: any = {}) {
	        return new IncrementalBackupConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enableBackup = source["enableBackup"];
	        this.backupPath = source["backupPath"];
	        this.lastBackupTime = source["lastBackupTime"];
	        this.maxBackupVersions = source["maxBackupVersions"];
	    }
	}
	export class NewMessageExportConfig {
	    enableExport: boolean;
	    startTime: number;
	    savePath: string;
	    includeMedia: boolean;
	    groupByContact: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NewMessageExportConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enableExport = source["enableExport"];
	        this.startTime = source["startTime"];
	        this.savePath = source["savePath"];
	        this.includeMedia = source["includeMedia"];
	        this.groupByContact = source["groupByContact"];
	    }
	}

}

