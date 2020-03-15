package populate

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/goombaio/namegenerator"
	"github.com/pivotal/kpack/pkg/apis/build/v1alpha1"
	"github.com/pivotal/kpack/pkg/client/clientset/versioned"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/matthewmcnew/build-service-visualization/defaults"
	"github.com/matthewmcnew/build-service-visualization/k8s"
)

func Populate(count int32, builder, registry, cacheSize string) {
	clusterConfig, err := k8s.BuildConfigFromFlags("", "")
	if err != nil {
		log.Fatalf("Error building kubeconfig: %v", err)
	}

	k8sclient, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		log.Fatalf("could not get Build client: %s", err)
	}

	client, err := versioned.NewForConfig(clusterConfig)
	if err != nil {
		log.Fatalf("could not get Build client: %s", err)
	}

	c := loadConfig(count, registry)

	_, err = k8sclient.CoreV1().Namespaces().Create(&v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: defaults.Namespace,
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Fatalf(err.Error())
	}

	secret, err := k8sclient.CoreV1().Secrets(defaults.Namespace).Create(&v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pbdemo-dockersecret-",
			Annotations: map[string]string{
				"build.pivotal.io/docker": c.registry,
			},
		},
		StringData: map[string]string{
			"username": c.username,
			"password": c.password,
		},
		Type: v1.SecretTypeBasicAuth,
	})
	noError(err)

	serviceAccount, err := k8sclient.CoreV1().ServiceAccounts(defaults.Namespace).Create(&v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pbdemo-serviceaccount-",
		},
		Secrets: []v1.ObjectReference{
			{
				Name: secret.Name,
			},
		},
	})
	noError(err)

	const builderName = defaults.BuilderName
	clusterBuilder, err := client.BuildV1alpha1().ClusterBuilders().Get(builderName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		noError(err)
	}

	if errors.IsNotFound(err) {
		_, err = client.BuildV1alpha1().ClusterBuilders().Create(&v1alpha1.ClusterBuilder{
			ObjectMeta: metav1.ObjectMeta{
				Name: builderName,
			},
			Spec: v1alpha1.BuilderSpec{
				Image:        builder,
				UpdatePolicy: v1alpha1.Polling,
			},
		})
		if err != nil {
			noError(err)
		}
	} else {
		_, err = client.BuildV1alpha1().ClusterBuilders().Update(&v1alpha1.ClusterBuilder{
			ObjectMeta: clusterBuilder.ObjectMeta,
			Spec: v1alpha1.BuilderSpec{
				Image:        builder,
				UpdatePolicy: v1alpha1.Polling,
			},
		})
		if err != nil && !errors.IsAlreadyExists(err) {
			noError(err)
		}
	}

	updatePbBuilder(builder, client)

	seed := time.Now().UTC().UnixNano()
	nameGenerator := namegenerator.NewNameGenerator(seed)

	cache, err := resource.ParseQuantity(cacheSize)
	if err != nil {
		log.Fatalf("error parsing cache size %s", cacheSize)
	}
	for i := 1; i <= c.count; i++ {

		sourceConfig, tag := randomSourceConfig()
		image, err := client.BuildV1alpha1().Images(defaults.Namespace).Create(&v1alpha1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Name: nameGenerator.Generate(),
			},
			Spec: v1alpha1.ImageSpec{
				Tag: fmt.Sprintf("%s:%s", c.imageTag, tag),
				Builder: v1.ObjectReference{
					Name: builderName,
					Kind: "ClusterBuilder",
				},
				ServiceAccount:       serviceAccount.Name,
				Source:               sourceConfig,
				CacheSize:            &cache,
				ImageTaggingStrategy: v1alpha1.None,
			},
		})
		if err != nil && !errors.IsAlreadyExists(err) {
			noError(err)
		} else if errors.IsAlreadyExists(err) {
			i--
			continue
		}

		log.Printf("created image %s", image.Name)
		time.Sleep(3 * time.Second)
	}

}

func noError(err error) {
	if err != nil {
		log.Fatal(err.Error())
	}
}

type config struct {
	builder      string
	testRegistry string
	imageTag     string
	username     string
	password     string
	registry     string
	count        int
}

func loadConfig(count int32, registry string) config {
	imageTag := registryTag(registry)

	reg, err := name.ParseReference(registry, name.WeakValidation)
	if err != nil {
		log.Fatalf("Could not parse %s", imageTag)
	}

	auth, err := authn.DefaultKeychain.Resolve(reg.Context().Registry)
	if err != nil {
		log.Fatalf("Could not find keychain for%s", imageTag)
	}

	basicAuth, err := auth.Authorization()
	if err != nil {
		log.Fatalf("Could not get auth for%s", imageTag)
	}

	return config{
		testRegistry: registry,
		username:     basicAuth.Username,
		password:     basicAuth.Password,
		count:        int(count),
		imageTag:     imageTag,
		registry: func() string {
			if reg.Context().RegistryStr() == name.DefaultRegistry {
				return "https://" + name.DefaultRegistry + "/v1/"
			}
			return reg.Context().RegistryStr()
		}(),
	}
}

func registryTag(registry string) string {
	return registry + "/pbdemo"
}

func randomSourceConfig() (v1alpha1.SourceConfig, string) {
	rand.Seed(time.Now().UnixNano())
	sourceConfigs := []v1alpha1.SourceConfig{
		{
			Git: &v1alpha1.Git{
				URL:      "https://github.com/matthewmcnew/sample-java-app",
				Revision: "dbba68cee6473b5df51a1a43806d920d2ed4e4ee",
			},
		},
		{
			Git: &v1alpha1.Git{
				URL:      "https://github.com/matthewmcnew/build-samples",
				Revision: "a94df327e098fe924b06547a1adf9c3cda5684c9",
			},
		},
		{
			Git: &v1alpha1.Git{
				URL:      "https://github.com/cloudfoundry/go-mod-cnb",
				Revision: "master",
			},
			SubPath: "integration/testdata/vendored",
		},
		{
			Git: &v1alpha1.Git{
				URL:      "https://github.com/cloudfoundry/dotnet-core-runtime-cnb",
				Revision: "master",
			},
			SubPath: "integration/testdata/simple_app",
		},
	}

	imageTypes := []string{
		"java",
		"node",
		"go",
		"maven",
		"java",
		"java",
	}

	randomIndex := rand.Intn(len(sourceConfigs))

	return sourceConfigs[randomIndex], imageTypes[randomIndex]
}

func updatePbBuilder(builderName string, client *versioned.Clientset) {
	builder, err := client.BuildV1alpha1().Builders("build-service-builds").Get("build-service-builder", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		noError(err)
	}

	if errors.IsNotFound(err) {
		return
	}

	_, err = client.BuildV1alpha1().Builders("build-service-builds").Update(&v1alpha1.Builder{
		ObjectMeta: builder.ObjectMeta,
		Spec: v1alpha1.BuilderWithSecretsSpec{
			BuilderSpec: v1alpha1.BuilderSpec{
				Image:        builderName,
				UpdatePolicy: v1alpha1.Polling,
			},
		},
	})
	if err != nil {
		noError(err)
	}

}
