package did

import (
	"crypto"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kenlabs/pando-id/pkg/internal/marshal"
	"github.com/kenlabs/pando-id/pkg/types"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/shengdoushi/base58"
)

type Document struct {
	Context              []types.URI               `json:"@context"`
	ID                   DID                       `json:"id"`
	Controller           []DID                     `json:"controller,omitempty"`
	VerificationMethod   VerificationMethods       `json:"verificationMethod,omitempty"`
	Authentication       VerificationRelationships `json:"authentication,omitempty"`
	AssertionMethod      VerificationRelationships `json:"assertionMethod,omitempty"`
	KeyAgreement         VerificationRelationships `json:"keyAgreement,omitempty"`
	CapabilityInvocation VerificationRelationships `json:"capabilityInvocation,omitempty"`
	CapabilityDelegation VerificationRelationships `json:"capabilityDelegation,omitempty"`
	Service              []Service                 `json:"service,omitempty"`
}

type VerificationMethods []*VerificationMethod

func (vms VerificationMethods) FindByID(id DID) *VerificationMethod {
	for _, vm := range vms {
		if vm.ID.Equals(id) {
			return vm
		}
	}
	return nil
}

func (vms *VerificationMethods) Remove(id DID) *VerificationMethod {
	var (
		filteredVMS []*VerificationMethod
		foundVM     *VerificationMethod
	)
	for _, vm := range *vms {
		if !vm.ID.Equals(id) {
			filteredVMS = append(filteredVMS, vm)
		} else {
			foundVM = vm
		}
	}
	*vms = filteredVMS
	return foundVM
}

func (vms *VerificationMethods) Add(v *VerificationMethod) {
	for _, ptr := range *vms {
		if ptr == v {
			return
		}
		if ptr.ID.Equals(v.ID) {
			return
		}
	}
	*vms = append(*vms, v)
}

type VerificationRelationships []VerificationRelationship

func (vmr VerificationRelationships) FindByID(id DID) *VerificationMethod {
	for _, r := range vmr {
		if r.VerificationMethod != nil {
			if r.VerificationMethod.ID.Equals(id) {
				return r.VerificationMethod
			}
		}
	}
	return nil
}

func (vmr *VerificationRelationships) Remove(id DID) *VerificationRelationship {
	var (
		filteredVMRels []VerificationRelationship
		removedRel     *VerificationRelationship
	)
	for _, r := range *vmr {
		if !r.ID.Equals(id) {
			filteredVMRels = append(filteredVMRels, r)
		} else {
			removedRel = &r
		}
	}
	*vmr = filteredVMRels
	return removedRel
}

func (vmr *VerificationRelationships) Add(vm *VerificationMethod) {
	for _, rel := range *vmr {
		if rel.ID.Equals(vm.ID) {
			return
		}
	}
	*vmr = append(*vmr, VerificationRelationship{vm, vm.ID})
}

func (d *Document) AddAuthenticationMethod(v *VerificationMethod) {
	if v.Controller.Empty() {
		v.Controller = d.ID
	}
	d.VerificationMethod.Add(v)
	d.Authentication.Add(v)
}

func (d *Document) AddAssertionMethod(v *VerificationMethod) {
	if v.Controller.Empty() {
		v.Controller = d.ID
	}
	d.VerificationMethod.Add(v)
	d.AssertionMethod.Add(v)
}

func (d *Document) AddKeyAgreement(v *VerificationMethod) {
	if v.Controller.Empty() {
		v.Controller = d.ID
	}
	d.VerificationMethod.Add(v)
	d.KeyAgreement.Add(v)
}

func (d *Document) AddCapabilityInvocation(v *VerificationMethod) {
	if v.Controller.Empty() {
		v.Controller = d.ID
	}
	d.VerificationMethod.Add(v)
	d.CapabilityInvocation.Add(v)
}

func (d *Document) AddCapabilityDelegation(v *VerificationMethod) {
	if v.Controller.Empty() {
		v.Controller = d.ID
	}
	d.VerificationMethod.Add(v)
	d.CapabilityDelegation.Add(v)
}

func (d Document) MarshalJSON() ([]byte, error) {
	type alias Document
	tmp := alias(d)
	if data, err := json.Marshal(tmp); err != nil {
		return nil, err
	} else {
		return marshal.NormalizeDocument(data, marshal.Unplural(contextKey), marshal.Unplural(controllerKey))
	}
}

func (d *Document) UnmarshalJSON(b []byte) error {
	type Alias Document
	normalizedDoc, err := marshal.NormalizeDocument(b, pluralContext, marshal.Plural(controllerKey))
	if err != nil {
		return err
	}
	doc := Alias{}
	err = json.Unmarshal(normalizedDoc, &doc)
	if err != nil {
		return err
	}
	*d = (Document)(doc)

	const errMsg = "unable to resolve all '%s' references: %w"
	if err = resolveVerificationRelationships(d.Authentication, d.VerificationMethod); err != nil {
		return fmt.Errorf(errMsg, authenticationKey, err)
	}
	if err = resolveVerificationRelationships(d.AssertionMethod, d.VerificationMethod); err != nil {
		return fmt.Errorf(errMsg, assertionMethodKey, err)
	}
	if err = resolveVerificationRelationships(d.KeyAgreement, d.VerificationMethod); err != nil {
		return fmt.Errorf(errMsg, keyAgreementKey, err)
	}
	if err = resolveVerificationRelationships(d.CapabilityInvocation, d.VerificationMethod); err != nil {
		return fmt.Errorf(errMsg, capabilityInvocationKey, err)
	}
	if err = resolveVerificationRelationships(d.CapabilityDelegation, d.VerificationMethod); err != nil {
		return fmt.Errorf(errMsg, capabilityDelegationKey, err)
	}
	return nil
}

func (d Document) IsController(controller DID) bool {
	if controller.Empty() {
		return false
	}
	for _, curr := range d.Controller {
		if curr.Equals(controller) {
			return true
		}
	}
	return false
}

func (d *Document) ResolveEndpointURL(serviceType string) (endpointID types.URI, endpointURL string, err error) {
	var services []Service
	for _, service := range d.Service {
		if service.Type == serviceType {
			services = append(services, service)
		}
	}
	if len(services) == 0 {
		return types.URI{}, "", fmt.Errorf("service not found (did=%s, type=%s)", d.ID, serviceType)
	}
	if len(services) > 1 {
		return types.URI{}, "", fmt.Errorf("multiple services found (did=%s, type=%s)", d.ID, serviceType)
	}
	err = services[0].UnmarshalServiceEndpoint(&endpointURL)
	if err != nil {
		return types.URI{}, "", fmt.Errorf("unable to unmarshal single URL from service (id=%s): %w", services[0].ID.String(), err)
	}
	return services[0].ID, endpointURL, nil
}

type Service struct {
	ID              types.URI   `json:"id"`
	Type            string      `json:"type,omitempty"`
	ServiceEndpoint interface{} `json:"serviceEndpoint,omitempty"`
}

func (s Service) MarshalJSON() ([]byte, error) {
	type alias Service
	tmp := alias(s)
	if data, err := json.Marshal(tmp); err != nil {
		return nil, err
	} else {
		return marshal.NormalizeDocument(data, marshal.Unplural(serviceEndpointKey))
	}
}

func (s *Service) UnmarshalJSON(data []byte) error {
	normalizedData, err := marshal.NormalizeDocument(data, pluralContext, marshal.PluralValueOrMap(serviceEndpointKey))
	if err != nil {
		return err
	}
	type alias Service
	var result alias
	if err := json.Unmarshal(normalizedData, &result); err != nil {
		return err
	}
	*s = (Service)(result)
	return nil
}

func (s Service) UnmarshalServiceEndpoint(target interface{}) error {
	var valueToMarshal interface{}
	if asSlice, ok := s.ServiceEndpoint.([]interface{}); ok && len(asSlice) == 1 {
		valueToMarshal = asSlice[0]
	} else {
		valueToMarshal = s.ServiceEndpoint
	}
	if asJSON, err := json.Marshal(valueToMarshal); err != nil {
		return err
	} else {
		return json.Unmarshal(asJSON, target)
	}
}

type VerificationMethod struct {
	ID              DID                    `json:"id"`
	Type            types.KeyType          `json:"type,omitempty"`
	Controller      DID                    `json:"controller,omitempty"`
	PublicKeyBase58 string                 `json:"publicKeyBase58,omitempty"`
	PublicKeyJwk    map[string]interface{} `json:"publicKeyJwk,omitempty"`
}

func NewVerificationMethod(id DID, keyType types.KeyType, controller DID, key crypto.PublicKey) (*VerificationMethod, error) {
	vm := &VerificationMethod{
		ID:         id,
		Type:       keyType,
		Controller: controller,
	}

	if keyType == types.JsonWebKey2020 {
		keyAsJWK, err := jwk.New(key)
		if err != nil {
			return nil, err
		}
		keyAsJSON, err := json.Marshal(keyAsJWK)
		if err != nil {
			return nil, err
		}
		keyAsMap := map[string]interface{}{}
		err = json.Unmarshal(keyAsJSON, &keyAsMap)
		if err != nil {
			return nil, err
		}

		vm.PublicKeyJwk = keyAsMap
	}
	if keyType == types.ED25519VerificationKey2018 {
		ed25519Key, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("wrong key type")
		}
		encodedKey := base58.Encode(ed25519Key, base58.BitcoinAlphabet)
		vm.PublicKeyBase58 = encodedKey
	}

	return vm, nil
}

func (v VerificationMethod) JWK() (jwk.Key, error) {
	if v.PublicKeyJwk == nil {
		return nil, nil
	}
	jwkAsJSON, _ := json.Marshal(v.PublicKeyJwk)
	key, err := jwk.ParseKey(jwkAsJSON)
	if err != nil {
		return nil, fmt.Errorf("could not parse public key: %w", err)
	}
	return key, nil
}

func (v VerificationMethod) PublicKey() (crypto.PublicKey, error) {
	var pubKey crypto.PublicKey
	switch v.Type {
	case types.ED25519VerificationKey2018:
		keyBytes, err := base58.Decode(v.PublicKeyBase58, base58.BitcoinAlphabet)
		if err != nil {
			return nil, err
		}
		return ed25519.PublicKey(keyBytes), err
	case types.JsonWebKey2020:
		keyAsJWK, err := v.JWK()
		if err != nil {
			return nil, err
		}
		err = keyAsJWK.Raw(&pubKey)
		if err != nil {
			return nil, err
		}
		return pubKey, nil
	}
	return nil, errors.New("unsupported verification method type")
}

type VerificationRelationship struct {
	*VerificationMethod
	reference DID
}

func (v VerificationRelationship) MarshalJSON() ([]byte, error) {
	if v.reference.Empty() {
		return json.Marshal(*v.VerificationMethod)
	} else {
		return json.Marshal(v.reference)
	}
}

func (v *VerificationRelationship) UnmarshalJSON(b []byte) error {
	type Alias VerificationRelationship
	switch b[0] {
	case '{':
		tmp := Alias{VerificationMethod: &VerificationMethod{}}
		err := json.Unmarshal(b, &tmp)
		if err != nil {
			return fmt.Errorf("could not parse verificationRelation method: %w", err)
		}
		*v = (VerificationRelationship)(tmp)
	case '"':
		err := json.Unmarshal(b, &v.reference)
		if err != nil {
			return fmt.Errorf("could not parse verificationRelation key relation DID: %w", err)
		}
	default:
		return errors.New("verificationRelation is invalid")
	}
	return nil
}

func (v *VerificationMethod) UnmarshalJSON(bytes []byte) error {
	type Alias VerificationMethod
	tmp := Alias{}
	err := json.Unmarshal(bytes, &tmp)
	if err != nil {
		return err
	}
	*v = (VerificationMethod)(tmp)
	return nil
}

func resolveVerificationRelationships(relationships []VerificationRelationship, methods []*VerificationMethod) error {
	for i, relationship := range relationships {
		if relationship.reference.Empty() {
			continue
		}
		if resolved := resolveVerificationRelationship(relationship.reference, methods); resolved == nil {
			return fmt.Errorf("unable to resolve %s: %s", verificationMethodKey, relationship.reference.String())
		} else {
			relationships[i] = *resolved
			relationships[i].reference = relationship.reference
		}
	}
	return nil
}

func resolveVerificationRelationship(reference DID, methods []*VerificationMethod) *VerificationRelationship {
	for _, method := range methods {
		if method.ID.Equals(reference) {
			return &VerificationRelationship{VerificationMethod: method}
		}
	}
	return nil
}