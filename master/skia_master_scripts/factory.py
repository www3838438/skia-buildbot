# Copyright (c) 2012 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""Utility class to build the Skia master BuildFactory's.

Based on gclient_factory.py and adds Skia-specific steps."""

import ntpath
import posixpath

from buildbot.process.properties import WithProperties
from master.factory import gclient_factory

from skia_master_scripts import commands as skia_commands
import config


SKIA_SVN_BASEURL = 'https://skia.googlecode.com/svn'
AUTOGEN_SVN_BASEURL = 'https://skia-autogen.googlecode.com/svn'

# TODO(epoger): My intent is to make the build steps identical on all platforms
# and thus remove the need for the whole target_platform parameter.
# For now, these must match the target_platform values used in
# third_party/chromium_buildbot/scripts/master/factory/gclient_factory.py ,
# because we pass these values into GClientFactory.__init__() .
TARGET_PLATFORM_LINUX = 'linux'
TARGET_PLATFORM_MAC = 'mac'
TARGET_PLATFORM_WIN32 = 'win32'

CONFIG_DEBUG = 'Debug'
CONFIG_RELEASE = 'Release'
CONFIG_BENCH = 'Bench'
CONFIGURATIONS = [CONFIG_DEBUG, CONFIG_RELEASE]

class SkiaFactory(gclient_factory.GClientFactory):
  """Encapsulates data and methods common to the Skia master.cfg files."""

  def __init__(self, do_upload_results=False,
               build_subdir='trunk', other_subdirs=None,
               target_platform=None, configuration=CONFIG_DEBUG,
               default_timeout=2400,
               environment_variables=None, gm_image_subdir=None,
               perf_output_basedir=None, builder_name=None, make_flags=None,
               test_args=None, gm_args=None, bench_args=None):
    """Instantiates a SkiaFactory as appropriate for this target_platform.

    do_upload_results: whether we should upload bench/gm results
    build_subdir: subdirectory to check out and then build within
    other_subdirs: list of other subdirectories to also check out (or None)
    target_platform: a string such as TARGET_PLATFORM_LINUX
    configuration: 'Debug' or 'Release'
    default_timeout: default timeout for each command, in seconds
    environment_variables: dictionary of environment variables that should
        be passed to all commands
    gm_image_subdir: directory containing images for comparison against results
        of gm tool
    perf_output_basedir: path to directory under which to store performance
        data, or None if we don't want to store performance data
    builder_name: name of the builder associated with this factory
    make_flags: list of extra flags to pass to the compile step
    test_args: list of extra flags to pass to the 'tests' executable
    gm_args: list of extra flags to pass to the 'gm' executable
    bench_args: list of extra flags to pass to the 'bench' executable
    """

    if not make_flags:
      make_flags = []
    self._make_flags = make_flags
    # Platform-specific stuff.
    if target_platform == TARGET_PLATFORM_WIN32:
      self.TargetPathJoin = ntpath.join
    else:
      self.TargetPathJoin = posixpath.join
      self._make_flags += ['--jobs', '--max-load=4.0']

    # Create gclient solutions corresponding to the main build_subdir
    # and other directories we also wish to check out.
    solutions = [gclient_factory.GClientSolution(
        svn_url=config.Master.skia_url + build_subdir, name=build_subdir)]
    if not other_subdirs:
      other_subdirs = []
    if gm_image_subdir:
      other_subdirs.append('gm-expected/%s' % gm_image_subdir)
    other_subdirs.append('skp')
    for other_subdir in other_subdirs:
      solutions.append(gclient_factory.GClientSolution(
          svn_url=config.Master.skia_url + other_subdir,
          name=other_subdir))
    gclient_factory.GClientFactory.__init__(
        self, build_dir='', solutions=solutions,
        target_platform=target_platform)

    self._factory = self.BaseFactory(factory_properties={'no_kill':True})

    # Set _default_clobber based on config.Master
    self._default_clobber = getattr(config.Master, 'default_clobber', False)

    self._do_upload_results = do_upload_results
    self._do_upload_bench_results = do_upload_results and \
        perf_output_basedir != None

    # Get an implementation of SkiaCommands as appropriate for
    # this target_platform.
    workdir = self.TargetPathJoin('build', build_subdir)
    self._skia_cmd_obj = skia_commands.SkiaCommands(
        target_platform=target_platform, factory=self._factory,
        configuration=configuration, workdir=workdir,
        target_arch=None, default_timeout=default_timeout,
        environment_variables=environment_variables)

    self._perf_output_basedir = perf_output_basedir

    self._configuration = configuration
    if self._configuration not in CONFIGURATIONS:
      raise ValueError('Invalid configuration %s.  Must be one of: %s' % (
          self._configuration, CONFIGURATIONS))

    self._skia_svn_username_file = '.skia_svn_username'
    self._skia_svn_password_file = '.skia_svn_password'
    self._autogen_svn_username_file = '.autogen_svn_username'
    self._autogen_svn_password_file = '.autogen_svn_password'
    self._builder_name = builder_name

    if not test_args:
      test_args = []
    if not gm_args:
      gm_args = []
    if not bench_args:
      bench_args = []
    self._common_args = ['--autogen_svn_baseurl', AUTOGEN_SVN_BASEURL,
                         '--configuration', configuration,
                         '--gm_image_subdir', gm_image_subdir or 'None',
                         '--builder_name', builder_name,
                         '--target_platform', target_platform,
                         '--revision', WithProperties('%(got_revision)s'),
                         '--perf_output_basedir', perf_output_basedir or 'None',
                         '--make_flags', '"%s"' % ' '.join(self._make_flags),
                         '--test_args', '"%s"' % ' '.join(test_args),
                         '--gm_args', '"%s"' % ' '.join(gm_args),
                         '--bench_args', '"%s"' % ' '.join(bench_args),
                         ]

  def AddSlaveScript(self, script, description, args=[], timeout=None,
                     halt_on_failure=False):
    self._skia_cmd_obj.AddSlaveScript(script=script,
                                      args=self._common_args + args,
                                      description=description,
                                      timeout=timeout,
                                      halt_on_failure=halt_on_failure)

  def Make(self, target, description):
    """Build a single target."""
    args = ['--target', target]
    self.AddSlaveScript(script='compile.py', args=args,
                        description=description, halt_on_failure=True)

  def Compile(self):
    """Compile step.  Build everything. """
    self.Make('skia_base_libs',  'BuildSkiaBaseLibs')
    self.Make('tests', 'BuildTests')
    self.Make('gm',    'BuildGM')
    self.Make('tools', 'BuildTools')
    self.Make('bench', 'BuildBench')
    self.Make('most',  'BuildMost')

  def RunTests(self):
    """ Run the unit tests. """
    self.AddSlaveScript(script='run_tests.py', description='RunTests')

  def RunGM(self):
    """ Run the "GM" tool, saving the images to disk. """
    self.AddSlaveScript(script='run_gm.py', description='GenerateGMs')

  def RenderPictures(self):
    """ Run the "render_pictures" tool to generate images from .skp's. """
    self.AddSlaveScript(script='render_pictures.py',
                        description='RenderPictures')

  def CompareGMs(self):
    """ Run the "skdiff" tool to compare the "actual" GM images we just
    generated to the baselines in _gm_image_subdir. """
    self.AddSlaveScript(script='compare_gms.py', description='CompareGMs')

  def RunBench(self):
    """ Run "bench", piping the output somewhere so we can graph
    results over time. """
    self.AddSlaveScript(script='run_bench.py', description='RunBench')

  def BenchPictures(self):
    """ Run "bench_pictures" """
    self.AddSlaveScript(script='bench_pictures.py', description='BenchPictures')

  def BenchGraphs(self):
    """ Generate bench performance graphs. """
    self.AddSlaveScript(script='generate_bench_graphs.py',
                        description='GenerateBenchGraphs')

  def UploadBenchGraphs(self):
    """ Upload bench performance graphs (but only if we have been
    recording bench output for this build type). """
    self.AddSlaveScript(script='upload_bench_graphs.py',
                        description='UploadBenchGraphs')

  def UploadBenchResults(self):
    """ Upload bench results (performance data). """
    self.AddSlaveScript(script='upload_bench_results.py',
                        description='UploadBenchResults')

  def UploadGMResults(self):
    """ Upload the images generated by GM """
    args = ['--autogen_svn_username_file', self._autogen_svn_username_file,
            '--autogen_svn_password_file', self._autogen_svn_password_file]
    self.AddSlaveScript(script='upload_gm_results.py', args=args,
                        description='UploadGMResults', timeout=5400)

  def Build(self, clobber=None):
    """Build and return the complete BuildFactory.

    clobber: boolean indicating whether we should clean before building
    """
    # Do all the build steps first, so we will find out about build breakages
    # as soon as possible.
    if clobber is None:
      clobber = self._default_clobber
    if clobber:
      self.AddSlaveScript(script='clean.py', description='Clean')
    self.Compile()
    self.RunTests()
    self.RunGM()
    self.RenderPictures()
    if self._do_upload_results:
      self.UploadGMResults()
    self.CompareGMs()
    self.RunBench()
    self.BenchPictures()
    if self._do_upload_bench_results:
      self.UploadBenchResults()
      self.BenchGraphs()
      self.UploadBenchGraphs()

    return self._factory
