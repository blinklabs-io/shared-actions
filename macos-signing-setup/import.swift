import Darwin
import Foundation
import Security

func fail(_ operation: String, _ status: OSStatus? = nil) -> Never {
    var message = operation
    if let status = status {
        let detail = SecCopyErrorMessageString(status, nil) as String? ?? "unknown error"
        message += ": \(detail) (\(status))"
    }
    FileHandle.standardError.write(Data("\(message)\n".utf8))
    exit(EXIT_FAILURE)
}

let environment = ProcessInfo.processInfo.environment
guard let password = environment["P12_PASSWORD"],
      let path = environment["P12_PATH"],
      let trustedPath = environment["TRUSTED_APPLICATION"] else {
    fail("PKCS#12 import environment is incomplete")
}

let data: Data
do {
    data = try Data(contentsOf: URL(fileURLWithPath: path))
} catch {
    fail("could not read PKCS#12 file: \(error.localizedDescription)")
}

var keychain: SecKeychain?
var status = SecKeychainCopyDefault(&keychain)
guard status == errSecSuccess, let keychain = keychain else {
    fail("could not open the default keychain", status)
}

var trustedApplication: SecTrustedApplication?
status = trustedPath.withCString {
    SecTrustedApplicationCreateFromPath($0, &trustedApplication)
}
guard status == errSecSuccess, let trustedApplication = trustedApplication else {
    fail("could not create signing access", status)
}

var access: SecAccess?
status = SecAccessCreate(
    "Release signing" as CFString,
    [trustedApplication] as CFArray,
    &access
)
guard status == errSecSuccess, let access = access else {
    fail("could not create signing access", status)
}

let options: [String: Any] = [
    kSecImportExportPassphrase as String: password,
    kSecImportExportKeychain as String: keychain,
    kSecImportExportAccess as String: access,
]
var importedItems: CFArray?
status = SecPKCS12Import(data as CFData, options as CFDictionary, &importedItems)
guard status == errSecSuccess else {
    fail("could not import PKCS#12 identity", status)
}
guard let importedItems = importedItems as? [[String: Any]],
      importedItems.contains(where: {
          guard let item = $0[kSecImportItemIdentity as String] else {
              return false
          }
          return CFGetTypeID(item as AnyObject) == SecIdentityGetTypeID()
      }) else {
    fail("PKCS#12 import returned no identities")
}
