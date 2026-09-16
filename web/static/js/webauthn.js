// WebAuthn base64url <-> ArrayBuffer plumbing shared by login.html (enroll /
// verify / passwordless) and account.html (add a passkey from settings) —
// docs/DECISIONS.md ADR-047.
(function () {
  function b64urlToBuf(b64url) {
    var b64 = b64url.replace(/-/g, '+').replace(/_/g, '/');
    while (b64.length % 4) b64 += '=';
    var bin = atob(b64);
    var buf = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
    return buf.buffer;
  }
  function bufToB64url(buf) {
    var bytes = new Uint8Array(buf);
    var bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function transformCreationOptions(pk) {
    pk.challenge = b64urlToBuf(pk.challenge);
    pk.user.id = b64urlToBuf(pk.user.id);
    if (pk.excludeCredentials) {
      pk.excludeCredentials = pk.excludeCredentials.map(function (c) {
        return $.extend({}, c, { id: b64urlToBuf(c.id) });
      });
    }
    return pk;
  }
  function transformRequestOptions(pk) {
    pk.challenge = b64urlToBuf(pk.challenge);
    if (pk.allowCredentials) {
      pk.allowCredentials = pk.allowCredentials.map(function (c) {
        return $.extend({}, c, { id: b64urlToBuf(c.id) });
      });
    }
    return pk;
  }
  function credentialToJSON(cred) {
    var out = { id: cred.id, rawId: bufToB64url(cred.rawId), type: cred.type };
    if (cred.response.attestationObject) {
      out.response = {
        attestationObject: bufToB64url(cred.response.attestationObject),
        clientDataJSON: bufToB64url(cred.response.clientDataJSON)
      };
    } else {
      out.response = {
        authenticatorData: bufToB64url(cred.response.authenticatorData),
        clientDataJSON: bufToB64url(cred.response.clientDataJSON),
        signature: bufToB64url(cred.response.signature),
        userHandle: cred.response.userHandle ? bufToB64url(cred.response.userHandle) : null
      };
    }
    return out;
  }
  window.DSWebAuthn = {
    transformCreationOptions: transformCreationOptions,
    transformRequestOptions: transformRequestOptions,
    credentialToJSON: credentialToJSON
  };
})();