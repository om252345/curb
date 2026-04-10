export const workspace = {
    workspaceFolders: []
};

export const window = {
    showInformationMessage: () => Promise.resolve(),
    showErrorMessage: () => Promise.resolve()
};

export class EventEmitter {
    fire() {}
    event = () => {};
}

export enum ExtensionContext {
}
