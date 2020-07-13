package nuxeo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"nuxeo-operator/pkg/apis/nuxeo/v1alpha1"
)

// TestBackingServiceECK defines a Nuxeo CR with one backing service for ECK. It creates two simulated secrets
// like the ECK Operator, and creates a shell deployment. Then it configures the deployment from the Nuxeo CR
// and verifies that the configuration was correct. It whould produce a secondary secret, two environment
// variables, a volume for the secondary secret, and a volume mount for the secondary secret keys.
func (suite *backingServiceSuite) TestBackingServiceECK() {
	var err error
	nux := suite.backingServiceSuiteNewNuxeoES()
	dep := genTestDeploymentForBackingSvc()
	err = createECKSecrets(suite)
	require.Nil(suite.T(), err, "Error creating orphaned PVC: %v")
	err = configureBackingServices(&suite.r, nux, &dep, log)
	require.Nil(suite.T(), err, "configureBackingServices returned non-nil")
	secondarySecretName := suite.nuxeoName + "-" + nux.Spec.BackingServices[0].Name + "-binding"
	secret := v1.Secret{}
	err = suite.r.client.Get(context.TODO(), types.NamespacedName{Name: secondarySecretName, Namespace: suite.namespace}, &secret)
	require.Nil(suite.T(), err, "configureBackingServices failed to generate secondary secret")
	// confirm keys for p12 and password
	// confirm volume and volume mount
	// get nuxeo.conf configmap
	// confirm configmap contents
}

// backingServiceSuite is the BackingService test suite structure
type backingServiceSuite struct {
	suite.Suite
	r          ReconcileNuxeo
	nuxeoName  string
	namespace  string
	caSecret   string
	passSecret string
	password   string
	caCert     string
}

// SetupSuite initializes the Fake client, a ReconcileNuxeo struct, and various test suite constants
func (suite *backingServiceSuite) SetupSuite() {
	suite.r = initUnitTestReconcile()
	suite.nuxeoName = "testnux"
	suite.namespace = "testns"
	suite.caSecret = "elastic-es-http-certs-public"
	suite.passSecret = "elastic-es-elastic-user"
	suite.password = "testing123"
	suite.caCert = esCaCert()
}

// AfterTest removes objects of the type being tested in this suite after each test
func (suite *backingServiceSuite) AfterTest(_, _ string) {
	obj := v1alpha1.Nuxeo{}
	_ = suite.r.client.DeleteAllOf(context.TODO(), &obj)
	objSecret := v1.Secret{}
	_ = suite.r.client.DeleteAllOf(context.TODO(), &objSecret)
}

// This function runs the BackingService unit test suite. It is called by 'go test' and will call every
// function in this file with a backingServiceSuite receiver that begins with "Test..."
func TestBackingServiceUnitTestSuite(t *testing.T) {
	suite.Run(t, new(backingServiceSuite))
}

// backingServiceSuiteNewNuxeoES creates a test Nuxeo struct with one backing service: ECK
func (suite *backingServiceSuite) backingServiceSuiteNewNuxeoES() *v1alpha1.Nuxeo {
	return &v1alpha1.Nuxeo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suite.nuxeoName,
			Namespace: suite.namespace,
		},
		Spec: v1alpha1.NuxeoSpec{
			BackingServices: []v1alpha1.BackingService{{
				Name: "elastic",
				Resources: []v1alpha1.BackingServiceResource{{
					Version: "v1",
					Kind:    "secret",
					Name:    suite.caSecret,
					Projections: []v1alpha1.ResourceProjection{{
						Key:   "tls.crt",
						Mount: "",
						Transform: v1alpha1.CertTransform{
							Type:     v1alpha1.CrtToTrustStore,
							Store:    "elastic.ca.p12",
							Password: "elastic.truststore.pass",
							PassEnv:  "ELASTIC_TS_PASS",
						},
					}},
				}, {
					Version: "v1",
					Kind:    "secret",
					Name:    suite.passSecret,
					Projections: []v1alpha1.ResourceProjection{{
						Key: "elastic",
						Env: "ELASTIC_PASSWORD",
					}},
				}},
				NuxeoConf: "elasticsearch.restClient.username=elastic" +
					"elasticsearch.restClient.password=${env:ELASTIC_PASSWORD}" +
					"elasticsearch.addressList=https://elastic-es-http:9200" +
					"elasticsearch.restClient.truststore.path=/etc/nuxeo-operator/binding/elastic/elastic.ca.p12" +
					"elasticsearch.restClient.truststore.password=${env:ELASTIC_TS_PASS}" +
					"elasticsearch.restClient.truststore.type=p12",
			}},
		},
	}
}

// genTestDeploymentForBackingSvc creates and returns a Deployment struct minimally configured to support this suite
func genTestDeploymentForBackingSvc() appsv1.Deployment {
	replicas := int32(1)
	dep := appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
	}
	return dep
}

func createECKSecrets(suite *backingServiceSuite) error {
	userSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suite.passSecret,
			Namespace: suite.namespace,
		},
		StringData: map[string]string{"elastic": suite.password},
		Type:       corev1.SecretTypeOpaque,
	}
	if err := suite.r.client.Create(context.TODO(), &userSecret); err != nil {
		return err
	}
	caSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suite.caSecret,
			Namespace: suite.namespace,
		},
		StringData: map[string]string{"elastic": suite.password},
		Type:       corev1.SecretTypeOpaque,
	}
	return suite.r.client.Create(context.TODO(), &caSecret)
}

func esCaCert() string {
	return "" +
		"-----BEGIN CERTIFICATE-----" +
		"MIIDmDCCAoCgAwIBAgIRAIIhobOevGOuKRgN8oYgUJwwDQYJKoZIhvcNAQELBQAw" +
		"KTEQMA4GA1UECxMHZWxhc3RpYzEVMBMGA1UEAxMMZWxhc3RpYy1odHRwMB4XDTIw" +
		"MDcxMjIwMzc0M1oXDTIxMDcxMjIwNDc0M1owPTEQMA4GA1UECxMHZWxhc3RpYzEp" +
		"MCcGA1UEAxMgZWxhc3RpYy1lcy1odHRwLmJhY2tpbmcuZXMubG9jYWwwggEiMA0G" +
		"CSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDBx1arym+A6ek0j5zTmqCfMCTgTPwo" +
		"hpY7vfISn780ErjK/gfWPp1aXYEEhvT81OuR8yadPiZJiN6wBQzOz2Ja9VlX/Uy2" +
		"4AQqKDWL4VCYHaG8HIsxGFlkqJQfIhKGljhnRri37lBhimoDvUAr/pZgZ2LHeTqm" +
		"IkHNXW/7AH9yCH39VQfVVNpfsvD0vjOZDuvKXYf1J5Mz7FYvtbYb8azEfUSF5bE6" +
		"lmgaW5KyyeT66zKQKoFeKzr6QVqtImAo9n41TKmm7ztxmCXQQLPoYrAcYWG8qjMI" +
		"nsa2ews4sJzSBVWsPi274/Ca67ypER97XxbiQ88VSvLeY21TpE4B5oH/AgMBAAGj" +
		"gaYwgaMwDgYDVR0PAQH/BAQDAgWgMB0GA1UdJQQWMBQGCCsGAQUFBwMBBggrBgEF" +
		"BQcDAjByBgNVHREEazBpgiBlbGFzdGljLWVzLWh0dHAuYmFja2luZy5lcy5sb2Nh" +
		"bIIPZWxhc3RpYy1lcy1odHRwghtlbGFzdGljLWVzLWh0dHAuYmFja2luZy5zdmOC" +
		"F2VsYXN0aWMtZXMtaHR0cC5iYWNraW5nMA0GCSqGSIb3DQEBCwUAA4IBAQCSIWTy" +
		"m1s01fhgXAPZ6XUpUZwkxsj0Ah7mndedcFvWIjnLnMHd86ZYa8AeqHiWOlS6zbog" +
		"SH2iv6VOXgxHn3Dwsb4DFvg4gIp+3x1+4e/60VmT2OBlLeu998ug4XslRjsqZqYc" +
		"YUrSi18C/rlYas98xLihWQf7S57tuYua4u+KzK3XFEOxgkgzWEJDC+BQ9pYZcJ/o" +
		"vBo4DB2DiVZyJ+b4x6yglVKGXr6zWGlcjeNflsAPx0H3kMdWRfu+LFMvwP/aWEhU" +
		"OjAcCtA75EGuWNUK2JSw6H3w5Zg0x0fH6wrtECZlfD7p5KWFYAW1W/NnlQDbngLA" +
		"W96Yx0SrW1jDRziV" +
		"-----END CERTIFICATE-----" +
		"-----BEGIN CERTIFICATE-----" +
		"MIIDHjCCAgagAwIBAgIQEEZD9zl4FpfPR6I4MPHFRTANBgkqhkiG9w0BAQsFADAp" +
		"MRAwDgYDVQQLEwdlbGFzdGljMRUwEwYDVQQDEwxlbGFzdGljLWh0dHAwHhcNMjAw" +
		"NzEyMjAzNzQzWhcNMjEwNzEyMjA0NzQzWjApMRAwDgYDVQQLEwdlbGFzdGljMRUw" +
		"EwYDVQQDEwxlbGFzdGljLWh0dHAwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEK" +
		"AoIBAQDQz+K8UDC8Qjb7SAXd6X805i/5TiYbWhrKRD87pBXkksdQJ4I7S/0fLpb7" +
		"Wn0a2oQ8A6bIWxG8Vt6V3xWgbeQd6u0Vxqvc471Ey9j43CiAZ1kCFzB7nXm2z0fL" +
		"kF8HhO1uUSsVt+eRbiw8vxOkjqDKWRADyz71p9ihqaNNb+3CAEAl0n3qK9GjJrFD" +
		"dJfktanEzM98kK+ZC/CrAeLmh9w4UBsA07OVgDMDXX4sQAsCTP9HnJAVVVt3bhac" +
		"izXq1+sshRhlnBvZB5ulAkzck55QpdFQXCWjJdayUe1dho3H/PeGCbRezSzyx1er" +
		"UXcdcHC6ebpAZNo9610nqhm9d4ZdAgMBAAGjQjBAMA4GA1UdDwEB/wQEAwIChDAd" +
		"BgNVHSUEFjAUBggrBgEFBQcDAQYIKwYBBQUHAwIwDwYDVR0TAQH/BAUwAwEB/zAN" +
		"BgkqhkiG9w0BAQsFAAOCAQEAH+dacGY+MLbAi3eb4a6SUKKJxD+5GmBBfNGbFPrP" +
		"j+2mJF7Gj5t/AjRrNbtzDMijdyAxaAE3sTZE6OwSEj6t+K9pwn1RutUgEBpcXU3v" +
		"0qL4ZBNeJejlxEKOme+aW5JWSQ9FBaemxntZhe9UebvphD6cxFQNl9fYsInnORnD" +
		"6FaD8s6Qd16viWrrj+blrg6jYozsCTzi9wDEwFLwsR1rkJYDIJA8g65v5I5BryMu" +
		"G7yx1ZUbM5FW350vtczOnLtD/xm4n1jY9M5xTVFDskJO1IBZLxdLjrSoUJc/6upb" +
		"B7kaNdr6ckmmy1HDE3ezg4ca9ufxm6QuBvesPfGUG5Ycqg==" +
		"-----END CERTIFICATE-----"
}